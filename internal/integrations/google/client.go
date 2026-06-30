package google

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/awomore/Pill4rsBE/internal/integrations"
)

const (
	defaultOAuthBaseURL = "https://oauth2.googleapis.com"
	defaultAdsBaseURL   = "https://googleads.googleapis.com"
	authDialogURL       = "https://accounts.google.com/o/oauth2/v2/auth"
	apiVersion          = "v17"
	adwordsScope        = "https://www.googleapis.com/auth/adwords"
)

// Client talks to the Google Ads API (REST). Because Google access tokens are
// short-lived, we store the long-lived refresh token and exchange it for an
// access token at call time — so the "accessToken" argument the adapter methods
// receive is actually the stored refresh token.
type Client struct {
	clientID        string
	clientSecret    string
	redirectURI     string
	developerToken  string
	loginCustomerID string

	httpClient   *http.Client
	oauthBaseURL string
	adsBaseURL   string
}

func NewClient(clientID, clientSecret, redirectURI, developerToken, loginCustomerID string) *Client {
	return &Client{
		clientID:        clientID,
		clientSecret:    clientSecret,
		redirectURI:     redirectURI,
		developerToken:  developerToken,
		loginCustomerID: loginCustomerID,
		httpClient:      &http.Client{Timeout: 20 * time.Second},
		oauthBaseURL:    defaultOAuthBaseURL,
		adsBaseURL:      defaultAdsBaseURL,
	}
}

var _ integrations.AdPlatform = (*Client)(nil)

// Platform identifies this adapter.
func (c *Client) Platform() string { return "google" }

// GetOAuthURL builds the Google OAuth consent URL (offline access to get a
// refresh token; adwords scope for the Ads API).
func (c *Client) GetOAuthURL(state string) string {
	params := url.Values{}
	params.Set("client_id", c.clientID)
	params.Set("redirect_uri", c.redirectURI)
	params.Set("response_type", "code")
	params.Set("scope", adwordsScope)
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

// ExchangeCodeForToken returns the long-lived REFRESH token as the stored token.
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

// accessToken exchanges a stored refresh token for a short-lived access token.
func (c *Client) accessToken(ctx context.Context, refreshToken string) (string, error) {
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

// FetchCustomerID returns the first accessible Google Ads customer id.
func (c *Client) FetchCustomerID(ctx context.Context, refreshToken string) (string, error) {
	at, err := c.accessToken(ctx, refreshToken)
	if err != nil {
		return "", err
	}
	raw, err := c.adsPost(ctx, at, "/customers:listAccessibleCustomers", map[string]any{})
	if err != nil {
		return "", err
	}
	var out struct {
		ResourceNames []string `json:"resourceNames"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("decode customers: %w", err)
	}
	if len(out.ResourceNames) == 0 {
		return "", fmt.Errorf("no accessible google ads customers")
	}
	return lastSegment(out.ResourceNames[0]), nil
}

// FetchCustomers lists every accessible Google Ads customer. (The list endpoint
// only returns ids, so the name is derived from the id.)
func (c *Client) FetchCustomers(ctx context.Context, refreshToken string) ([]integrations.Account, error) {
	at, err := c.accessToken(ctx, refreshToken)
	if err != nil {
		return nil, err
	}
	raw, err := c.adsPost(ctx, at, "/customers:listAccessibleCustomers", map[string]any{})
	if err != nil {
		return nil, err
	}
	var out struct {
		ResourceNames []string `json:"resourceNames"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode customers: %w", err)
	}
	res := make([]integrations.Account, 0, len(out.ResourceNames))
	for _, rn := range out.ResourceNames {
		id := lastSegment(rn)
		res = append(res, integrations.Account{ID: id, Name: "Customer " + id})
	}
	return res, nil
}

func (c *Client) adsGet(ctx context.Context, accessToken, path string) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.adsBaseURL+"/"+apiVersion+path, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	c.setAdsHeaders(req, accessToken)
	return c.adsDo(req)
}

func (c *Client) adsPost(ctx context.Context, accessToken, path string, body any) (json.RawMessage, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.adsBaseURL+"/"+apiVersion+path, bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAdsHeaders(req, accessToken)
	return c.adsDo(req)
}

func (c *Client) setAdsHeaders(req *http.Request, accessToken string) {
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("developer-token", c.developerToken)
	if c.loginCustomerID != "" {
		req.Header.Set("login-customer-id", c.loginCustomerID)
	}
}

func (c *Client) adsDo(req *http.Request) (json.RawMessage, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("google ads api error %d: %s", resp.StatusCode, string(raw))
	}
	return raw, nil
}

func (c *Client) search(ctx context.Context, accessToken, customerID, query string) (json.RawMessage, error) {
	return c.adsPost(ctx, accessToken, fmt.Sprintf("/customers/%s/googleAds:search", customerID), map[string]any{"query": query})
}

// mutateOne sends a single create operation and returns the new resource name.
func (c *Client) mutateOne(ctx context.Context, accessToken, path string, create map[string]any) (string, error) {
	body := map[string]any{"operations": []any{map[string]any{"create": create}}}
	raw, err := c.adsPost(ctx, accessToken, path, body)
	if err != nil {
		return "", err
	}
	var out struct {
		Results []struct {
			ResourceName string `json:"resourceName"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("decode mutate: %w", err)
	}
	if len(out.Results) == 0 || out.Results[0].ResourceName == "" {
		return "", fmt.Errorf("mutate returned no resource name")
	}
	return out.Results[0].ResourceName, nil
}

func lastSegment(resourceName string) string {
	if i := strings.LastIndex(resourceName, "/"); i >= 0 {
		return resourceName[i+1:]
	}
	return resourceName
}

func microAmount(major float64) string {
	return strconv.FormatInt(int64(major*1_000_000), 10)
}

func microStringToMajor(s string) float64 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0
	}
	return float64(n) / 1_000_000
}
