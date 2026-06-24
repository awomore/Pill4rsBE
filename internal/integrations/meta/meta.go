package meta

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/awomore/Pill4rsBE/internal/integrations"
)

const (
	defaultGraphBaseURL  = "https://graph.facebook.com"
	defaultDialogBaseURL = "https://www.facebook.com"
	defaultAPIVersion    = "v21.0"

	// scopes requested for reading and managing ads.
	oauthScopes = "ads_read,ads_management"
)

// Client implements integrations.AdPlatform for the Meta (Facebook) Graph API.
type Client struct {
	appID       string
	appSecret   string
	redirectURI string

	httpClient    *http.Client
	graphBaseURL  string
	dialogBaseURL string
	apiVersion    string
}

var _ integrations.AdPlatform = (*Client)(nil)

// NewClient builds a Meta Graph API client. redirectURI must point at this
// backend's own callback endpoint (META_REDIRECT_URI).
func NewClient(appID, appSecret, redirectURI string) *Client {
	return &Client{
		appID:         appID,
		appSecret:     appSecret,
		redirectURI:   redirectURI,
		httpClient:    &http.Client{Timeout: 15 * time.Second},
		graphBaseURL:  defaultGraphBaseURL,
		dialogBaseURL: defaultDialogBaseURL,
		apiVersion:    defaultAPIVersion,
	}
}

// GetOAuthURL builds the Facebook OAuth dialog URL.
func (c *Client) GetOAuthURL(state string) string {
	params := url.Values{}
	params.Set("client_id", c.appID)
	params.Set("redirect_uri", c.redirectURI)
	params.Set("state", state)
	params.Set("scope", oauthScopes)
	params.Set("response_type", "code")
	return fmt.Sprintf("%s/%s/dialog/oauth?%s", c.dialogBaseURL, c.apiVersion, params.Encode())
}

type tokenResponse struct {
	AccessToken string      `json:"access_token"`
	TokenType   string      `json:"token_type"`
	ExpiresIn   int64       `json:"expires_in"`
	Error       *graphError `json:"error"`
}

type graphError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    int    `json:"code"`
}

func (e *graphError) Error() string {
	return fmt.Sprintf("meta graph error %d (%s): %s", e.Code, e.Type, e.Message)
}

// ExchangeCodeForToken POSTs the authorization code to the Graph OAuth token
// endpoint and returns the resulting access token and its expiry.
func (c *Client) ExchangeCodeForToken(ctx context.Context, code string) (*integrations.Token, error) {
	endpoint := fmt.Sprintf("%s/%s/oauth/access_token", c.graphBaseURL, c.apiVersion)

	form := url.Values{}
	form.Set("client_id", c.appID)
	form.Set("client_secret", c.appSecret)
	form.Set("redirect_uri", c.redirectURI)
	form.Set("code", code)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read token response: %w", err)
	}

	var parsed tokenResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decode token response: %w", err)
	}
	if parsed.Error != nil {
		return nil, parsed.Error
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed: status %d: %s", resp.StatusCode, string(body))
	}
	if parsed.AccessToken == "" {
		return nil, fmt.Errorf("token response missing access_token")
	}

	token := &integrations.Token{AccessToken: parsed.AccessToken}
	if parsed.ExpiresIn > 0 {
		token.ExpiresAt = time.Now().Add(time.Duration(parsed.ExpiresIn) * time.Second)
	}
	return token, nil
}

type adAccountsResponse struct {
	Data []struct {
		ID        string `json:"id"`
		AccountID string `json:"account_id"`
	} `json:"data"`
	Error *graphError `json:"error"`
}

type meResponse struct {
	ID    string      `json:"id"`
	Error *graphError `json:"error"`
}

// FetchAdAccountID returns an external account identifier for the connected
// user. It prefers the first available ad account, falling back to the Meta
// user id so the ad_accounts row always has a non-empty external id.
func (c *Client) FetchAdAccountID(ctx context.Context, accessToken string) (string, error) {
	params := url.Values{}
	params.Set("fields", "account_id")
	params.Set("limit", "1")
	params.Set("access_token", accessToken)
	endpoint := fmt.Sprintf("%s/%s/me/adaccounts?%s", c.graphBaseURL, c.apiVersion, params.Encode())

	var accounts adAccountsResponse
	if err := c.getJSON(ctx, endpoint, &accounts); err != nil {
		return "", err
	}
	if accounts.Error != nil {
		return "", accounts.Error
	}
	if len(accounts.Data) > 0 {
		if accounts.Data[0].ID != "" {
			return accounts.Data[0].ID, nil
		}
		if accounts.Data[0].AccountID != "" {
			return "act_" + accounts.Data[0].AccountID, nil
		}
	}

	meParams := url.Values{}
	meParams.Set("fields", "id")
	meParams.Set("access_token", accessToken)
	meEndpoint := fmt.Sprintf("%s/%s/me?%s", c.graphBaseURL, c.apiVersion, meParams.Encode())

	var me meResponse
	if err := c.getJSON(ctx, meEndpoint, &me); err != nil {
		return "", err
	}
	if me.Error != nil {
		return "", me.Error
	}
	if me.ID == "" {
		return "", fmt.Errorf("no ad account or user id available")
	}
	return me.ID, nil
}

func (c *Client) getJSON(ctx context.Context, endpoint string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
