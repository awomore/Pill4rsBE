package tiktok

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/awomore/Pill4rsBE/internal/integrations"
)

const (
	defaultBaseURL = "https://business-api.tiktok.com"
	apiPath        = "/open_api/v1.3"
)

// Client talks to the TikTok Business (Marketing) API.
type Client struct {
	appID       string
	appSecret   string
	redirectURI string
	httpClient  *http.Client
	baseURL     string
}

func NewClient(appID, appSecret, redirectURI string) *Client {
	return &Client{
		appID:       appID,
		appSecret:   appSecret,
		redirectURI: redirectURI,
		httpClient:  &http.Client{Timeout: 15 * time.Second},
		baseURL:     defaultBaseURL,
	}
}

var _ integrations.AdPlatform = (*Client)(nil)

// Platform identifies this adapter.
func (c *Client) Platform() string { return "tiktok" }

// GetOAuthURL builds the TikTok authorization URL.
func (c *Client) GetOAuthURL(state string) string {
	params := url.Values{}
	params.Set("app_id", c.appID)
	params.Set("redirect_uri", c.redirectURI)
	params.Set("state", state)
	return fmt.Sprintf("%s/portal/auth?%s", c.baseURL, params.Encode())
}

// envelope is TikTok's standard response wrapper.
type envelope struct {
	Code      int             `json:"code"`
	Message   string          `json:"message"`
	Data      json.RawMessage `json:"data"`
	RequestID string          `json:"request_id"`
}

type tokenData struct {
	AccessToken   string   `json:"access_token"`
	AdvertiserIDs []string `json:"advertiser_ids"`
}

// ExchangeCodeForToken swaps an auth code for an access token.
func (c *Client) ExchangeCodeForToken(ctx context.Context, code string) (*integrations.Token, error) {
	data, err := c.post(ctx, "/oauth2/access_token/", "", map[string]any{
		"app_id":     c.appID,
		"secret":     c.appSecret,
		"auth_code":  code,
		"grant_type": "authorization_code",
	})
	if err != nil {
		return nil, err
	}
	var td tokenData
	if err := json.Unmarshal(data, &td); err != nil {
		return nil, fmt.Errorf("decode token: %w", err)
	}
	if td.AccessToken == "" {
		return nil, fmt.Errorf("token response missing access_token")
	}
	// TikTok access tokens are long-lived; no expiry is returned here.
	return &integrations.Token{AccessToken: td.AccessToken}, nil
}

type advertiserListData struct {
	List []struct {
		AdvertiserID   string `json:"advertiser_id"`
		AdvertiserName string `json:"advertiser_name"`
	} `json:"list"`
}

// FetchAdvertiserID returns the first advertiser account the token can access.
func (c *Client) FetchAdvertiserID(ctx context.Context, accessToken string) (string, error) {
	params := url.Values{}
	params.Set("app_id", c.appID)
	params.Set("secret", c.appSecret)
	data, err := c.get(ctx, "/oauth2/advertiser/get/", accessToken, params)
	if err != nil {
		return "", err
	}
	var ad advertiserListData
	if err := json.Unmarshal(data, &ad); err != nil {
		return "", fmt.Errorf("decode advertisers: %w", err)
	}
	if len(ad.List) == 0 {
		return "", fmt.Errorf("no advertiser accounts available")
	}
	return ad.List[0].AdvertiserID, nil
}

// FetchAdvertisers lists every advertiser account the token can access.
func (c *Client) FetchAdvertisers(ctx context.Context, accessToken string) ([]integrations.Account, error) {
	params := url.Values{}
	params.Set("app_id", c.appID)
	params.Set("secret", c.appSecret)
	data, err := c.get(ctx, "/oauth2/advertiser/get/", accessToken, params)
	if err != nil {
		return nil, err
	}
	var ad advertiserListData
	if err := json.Unmarshal(data, &ad); err != nil {
		return nil, fmt.Errorf("decode advertisers: %w", err)
	}
	out := make([]integrations.Account, 0, len(ad.List))
	for _, a := range ad.List {
		out = append(out, integrations.Account{ID: a.AdvertiserID, Name: a.AdvertiserName})
	}
	return out, nil
}

func (c *Client) post(ctx context.Context, path, accessToken string, body any) (json.RawMessage, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+apiPath+path, bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if accessToken != "" {
		req.Header.Set("Access-Token", accessToken)
	}
	return c.do(req)
}

func (c *Client) get(ctx context.Context, path, accessToken string, params url.Values) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+apiPath+path+"?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if accessToken != "" {
		req.Header.Set("Access-Token", accessToken)
	}
	return c.do(req)
}

func (c *Client) do(req *http.Request) (json.RawMessage, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if env.Code != 0 {
		return nil, fmt.Errorf("tiktok api error %d: %s", env.Code, env.Message)
	}
	return env.Data, nil
}
