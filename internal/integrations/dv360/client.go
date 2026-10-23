// Package dv360 integrates the Display & Video 360 API (v5) as the DSP buying
// backbone. Campaigns fan out to DV360 as the native hierarchy
// campaign -> insertion order -> line item. Reporting uses the Bid Manager API.
package dv360

import (
	"bytes"
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
	defaultOAuthBaseURL     = "https://oauth2.googleapis.com"
	defaultAPIBaseURL       = "https://displayvideo.googleapis.com"
	defaultReportsBaseURL   = "https://doubleclickbidmanager.googleapis.com"
	authDialogURL           = "https://accounts.google.com/o/oauth2/v2/auth"
	apiVersion              = "v5"
	reportsVersion          = "v2"
	dv360Scope              = "https://www.googleapis.com/auth/display-video"
	bidManagerScope         = "https://www.googleapis.com/auth/doubleclickbidmanager"
	storageScope            = "https://www.googleapis.com/auth/devstorage.read_only"
	dv360Platform           = "dv360"
	defaultHTTPTimeout      = 30 * time.Second
	defaultReportingTimeout = 60 * time.Second
)

// Client talks to the DV360 API and Bid Manager API. Google access tokens are
// short-lived, so the adapter receives and stores the long-lived refresh token;
// every call exchanges it for an access token first.
type Client struct {
	clientID     string
	clientSecret string
	redirectURI  string

	httpClient        *http.Client
	reportsHTTPClient *http.Client
	oauthBaseURL      string
	apiBaseURL        string
	reportsBaseURL    string
	gcsBaseURL        string
}

func NewClient(clientID, clientSecret, redirectURI string) *Client {
	return &Client{
		clientID:          clientID,
		clientSecret:      clientSecret,
		redirectURI:       redirectURI,
		httpClient:        &http.Client{Timeout: defaultHTTPTimeout},
		reportsHTTPClient: &http.Client{Timeout: defaultReportingTimeout},
		oauthBaseURL:      defaultOAuthBaseURL,
		apiBaseURL:        defaultAPIBaseURL,
		reportsBaseURL:    defaultReportsBaseURL,
		gcsBaseURL:        "https://storage.googleapis.com",
	}
}

var (
	_ integrations.AdPlatform         = (*Client)(nil)
	_ integrations.CampaignAdapter    = (*Client)(nil)
	_ integrations.CampaignDataSource = (*Client)(nil)
)

// Platform identifies this adapter.
func (c *Client) Platform() string { return dv360Platform }

// Configured reports whether the OAuth client is usable.
func (c *Client) Configured() bool {
	return c.clientID != "" && c.clientSecret != "" && c.redirectURI != ""
}

// GetOAuthURL builds the Google consent URL. Both the DV360 entity scope and
// the Bid Manager reporting scope are requested in a single consent.
func (c *Client) GetOAuthURL(state string) string {
	params := url.Values{}
	params.Set("client_id", c.clientID)
	params.Set("redirect_uri", c.redirectURI)
	params.Set("response_type", "code")
	params.Set("scope", dv360Scope+" "+bidManagerScope+" "+storageScope)
	params.Set("access_type", "offline")
	params.Set("prompt", "consent")
	params.Set("state", state)
	return authDialogURL + "?" + params.Encode()
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

// ExchangeCodeForToken returns the long-lived refresh token as the stored token.
func (c *Client) ExchangeCodeForToken(ctx context.Context, code string) (*integrations.Token, error) {
	tr, err := c.tokenRequest(ctx, url.Values{
		"code":          {code},
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
		"redirect_uri":  {c.redirectURI},
		"grant_type":    {"authorization_code"},
	})
	if err != nil {
		return nil, err
	}
	if tr.RefreshToken == "" {
		return nil, fmt.Errorf("google did not return a refresh token (re-consent with prompt=consent required)")
	}
	return &integrations.Token{AccessToken: tr.RefreshToken}, nil
}

// AccessToken exchanges a stored refresh token for a short-lived access token.
func (c *Client) AccessToken(ctx context.Context, refreshToken string) (string, error) {
	tr, err := c.tokenRequest(ctx, url.Values{
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
		"refresh_token": {refreshToken},
		"grant_type":    {"refresh_token"},
	})
	if err != nil {
		return "", err
	}
	if tr.AccessToken == "" {
		return "", fmt.Errorf("token refresh returned no access_token")
	}
	return tr.AccessToken, nil
}

func (c *Client) tokenRequest(ctx context.Context, form url.Values) (*tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.oauthBaseURL+"/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read token response: %w", err)
	}
	var tr tokenResponse
	if err := json.Unmarshal(raw, &tr); err != nil {
		return nil, fmt.Errorf("decode token: %w", err)
	}
	if tr.Error != "" {
		return nil, fmt.Errorf("google oauth error: %s: %s", tr.Error, tr.ErrorDesc)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token request failed: status %d: %s", resp.StatusCode, string(raw))
	}
	return &tr, nil
}

// apiURL builds a versioned DV360 URL. path is relative to the version root.
func (c *Client) apiURL(path string) string {
	return c.apiBaseURL + "/" + apiVersion + path
}

// reportsURL builds a versioned Bid Manager URL.
func (c *Client) reportsURL(path string) string {
	return c.reportsBaseURL + "/" + reportsVersion + path
}

func (c *Client) getJSON(ctx context.Context, accessToken, rawURL string, out any) error {
	return c.doJSON(ctx, c.httpClient, http.MethodGet, rawURL, accessToken, nil, out)
}

func (c *Client) postJSON(ctx context.Context, accessToken, rawURL string, body, out any) error {
	return c.doJSON(ctx, c.httpClient, http.MethodPost, rawURL, accessToken, body, out)
}

func (c *Client) patchJSON(ctx context.Context, accessToken, rawURL string, body, out any) error {
	return c.doJSON(ctx, c.httpClient, http.MethodPatch, rawURL, accessToken, body, out)
}

func (c *Client) doJSON(ctx context.Context, client *http.Client, method, rawURL, accessToken string, body, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal body: %w", err)
		}
		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, rawURL, reader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("dv360 api error %d: %s", resp.StatusCode, string(raw))
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// getRaw returns the raw response body (used for CSV report downloads).
func (c *Client) getRaw(ctx context.Context, client *http.Client, rawURL, accessToken string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("dv360 api error %d: %s", resp.StatusCode, string(raw))
	}
	return raw, nil
}

// listAll walks a paginated DV360 list endpoint. key is the response field that
// holds the items (e.g. "campaigns").
func (c *Client) listAll(ctx context.Context, accessToken, rawURL, key string) ([]json.RawMessage, error) {
	var out []json.RawMessage
	pageToken := ""
	for {
		pageURL := rawURL
		sep := "?"
		if strings.Contains(pageURL, "?") {
			sep = "&"
		}
		pageURL += sep + "pageSize=100"
		if pageToken != "" {
			pageURL += "&pageToken=" + url.QueryEscape(pageToken)
		}

		var wrapper map[string]json.RawMessage
		if err := c.getJSON(ctx, accessToken, pageURL, &wrapper); err != nil {
			return nil, err
		}
		var items []json.RawMessage
		if raw, ok := wrapper[key]; ok {
			if err := json.Unmarshal(raw, &items); err != nil {
				return nil, fmt.Errorf("decode %s: %w", key, err)
			}
		}
		out = append(out, items...)

		next := ""
		if raw, ok := wrapper["nextPageToken"]; ok {
			_ = json.Unmarshal(raw, &next)
		}
		if next == "" {
			break
		}
		pageToken = next
	}
	return out, nil
}

func decodeAll[T any](items []json.RawMessage) ([]T, error) {
	out := make([]T, 0, len(items))
	for _, raw := range items {
		var v T
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}
