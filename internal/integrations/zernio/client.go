// Package zernio integrates the Zernio unified ads API (https://zernio.com),
// which manages paid campaigns across Meta, Google, TikTok, LinkedIn, Pinterest,
// X and OpenAI Ads behind one Bearer-authenticated REST API. One adapter is
// registered per ad network so the rest of Pill4rs (sync, campaign engine,
// Oma's review) works unchanged.
package zernio

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
)

// DefaultBaseURL is Zernio's production API base (paths are /v1/...).
const DefaultBaseURL = "https://zernio.com/api"

// Zernio ads platform values (the `platform` on a connected account).
const (
	PlatformMetaAds      = "metaads"
	PlatformGoogleAds    = "googleads"
	PlatformTikTokAds    = "tiktokads"
	PlatformLinkedInAds  = "linkedinads"
	PlatformPinterestAds = "pinterestads"
	PlatformXAds         = "xads"
	PlatformOpenAIAds    = "openaiads"
)

// Client is a thin Zernio REST client authenticated with a Bearer API key.
type Client struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

func NewClient(apiKey, baseURL string) *Client {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		apiKey:     strings.TrimSpace(apiKey),
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// Configured reports whether an API key is present.
func (c *Client) Configured() bool { return c != nil && c.apiKey != "" }

type apiError struct {
	Message string `json:"error"`
	Code    string `json:"code"`
	Type    string `json:"type"`
}

func (e apiError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("zernio: %s (%s)", e.Message, e.Code)
	}
	return "zernio: " + e.Message
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body any) (json.RawMessage, error) {
	endpoint := c.baseURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}

	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 300 {
		var ae apiError
		if json.Unmarshal(raw, &ae) == nil && ae.Message != "" {
			return nil, ae
		}
		return nil, fmt.Errorf("zernio api error %d: %s", resp.StatusCode, string(raw))
	}
	return raw, nil
}

// idField decodes an id that Zernio returns either as a plain string or as a
// populated object like {"_id":"..."}.
type idField string

func (f *idField) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*f = idField(s)
		return nil
	}
	var obj struct {
		ID string `json:"_id"`
	}
	if err := json.Unmarshal(b, &obj); err != nil {
		return err
	}
	*f = idField(obj.ID)
	return nil
}

// Account is a connected Zernio account (social or ads).
type Account struct {
	ID          string  `json:"_id"`
	Platform    string  `json:"platform"`
	ProfileID   idField `json:"profileId"`
	Username    string  `json:"username"`
	DisplayName string  `json:"displayName"`
	IsActive    bool    `json:"isActive"`
}

// ListAccounts returns the accounts connected to a profile (all profiles when
// profileID is empty).
func (c *Client) ListAccounts(ctx context.Context, profileID string) ([]Account, error) {
	q := url.Values{}
	if profileID != "" {
		q.Set("profileId", profileID)
	}
	raw, err := c.do(ctx, http.MethodGet, "/v1/accounts", q, nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Accounts []Account `json:"accounts"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode accounts: %w", err)
	}
	return out.Accounts, nil
}

// Profile is a Zernio profile (a named group of accounts).
type Profile struct {
	ID   string `json:"_id"`
	Name string `json:"name"`
}

// ListProfiles returns all profiles on the account.
func (c *Client) ListProfiles(ctx context.Context) ([]Profile, error) {
	raw, err := c.do(ctx, http.MethodGet, "/v1/profiles", nil, nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Profiles []Profile `json:"profiles"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode profiles: %w", err)
	}
	return out.Profiles, nil
}

// CreateProfile creates a profile (used to give each Pill4rs workspace its own
// Zernio account group).
func (c *Client) CreateProfile(ctx context.Context, name string) (Profile, error) {
	raw, err := c.do(ctx, http.MethodPost, "/v1/profiles", nil, map[string]any{"name": name})
	if err != nil {
		return Profile{}, err
	}
	var out struct {
		Profile Profile `json:"profile"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return Profile{}, fmt.Errorf("decode profile: %w", err)
	}
	return out.Profile, nil
}

// ConnectResult is the response from starting an ads OAuth connection.
type ConnectResult struct {
	AuthURL          string `json:"authUrl"`
	State            string `json:"state"`
	AlreadyConnected bool   `json:"alreadyConnected"`
	AccountID        string `json:"accountId"`
	Platform         string `json:"platform"`
}

// AdsConnectURL starts the unified ads-connection flow for a posting platform
// (facebook, instagram, linkedin, tiktok, twitter, pinterest, googleads).
func (c *Client) AdsConnectURL(ctx context.Context, postingPlatform, profileID, redirectURL string) (ConnectResult, error) {
	q := url.Values{}
	if profileID != "" {
		q.Set("profileId", profileID)
	}
	if redirectURL != "" {
		q.Set("redirect_url", redirectURL)
	}
	raw, err := c.do(ctx, http.MethodGet, "/v1/connect/"+postingPlatform+"/ads", q, nil)
	if err != nil {
		return ConnectResult{}, err
	}
	var out ConnectResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return ConnectResult{}, fmt.Errorf("decode connect: %w", err)
	}
	return out, nil
}

// IsAdsPlatform reports whether a Zernio platform value is a paid-ads network
// this integration services.
func IsAdsPlatform(p string) bool {
	switch p {
	case PlatformMetaAds, PlatformGoogleAds, PlatformTikTokAds, PlatformLinkedInAds,
		PlatformPinterestAds, PlatformXAds, PlatformOpenAIAds:
		return true
	default:
		return false
	}
}

// DisplayPlatform maps a Zernio ads platform value to the display `platform`
// enum used by the ads endpoints (facebook, google, tiktok, ...).
func DisplayPlatform(adsPlatform string) string {
	switch adsPlatform {
	case PlatformMetaAds:
		return "facebook"
	case PlatformGoogleAds:
		return "google"
	case PlatformTikTokAds:
		return "tiktok"
	case PlatformLinkedInAds:
		return "linkedin"
	case PlatformPinterestAds:
		return "pinterest"
	case PlatformXAds:
		return "twitter"
	case PlatformOpenAIAds:
		return "openai"
	default:
		return adsPlatform
	}
}
