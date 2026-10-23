package dv360

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

// Audience is the unified shape returned for every DV360 audience type.
type Audience struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
	Size string `json:"size,omitempty"`
}

// AudiencePage is a single page of audiences.
type AudiencePage struct {
	Audiences     []Audience `json:"audiences"`
	NextPageToken string     `json:"next_page_token,omitempty"`
}

// ListAudiences returns one page of an advertiser's audiences. kind is one of
// first_party, google, combined, custom_list, lookalike.
func (c *Client) ListAudiences(ctx context.Context, refreshToken, advertiserID, kind, pageToken string, pageSize int) (AudiencePage, error) {
	at, err := c.AccessToken(ctx, refreshToken)
	if err != nil {
		return AudiencePage{}, err
	}
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 100
	}

	q := url.Values{}
	q.Set("pageSize", strconv.Itoa(pageSize))
	if pageToken != "" {
		q.Set("pageToken", pageToken)
	}

	var path, key string
	switch kind {
	case "first_party", "first_party_and_partner", "":
		path, key = "/firstPartyAndPartnerAudiences", "firstPartyAndPartnerAudiences"
		q.Set("advertiserId", advertiserID)
	case "google":
		path, key = "/googleAudiences", "googleAudiences"
		q.Set("advertiserId", advertiserID)
	case "combined":
		path, key = "/combinedAudiences", "combinedAudiences"
		q.Set("advertiserId", advertiserID)
	case "custom_list", "custom_lists":
		path, key = "/customLists", "customLists"
		q.Set("advertiserId", advertiserID)
	case "lookalike", "lookalike_audience":
		path, key = "/lookalikeAudiences", "lookalikeAudiences"
		q.Set("advertiserId", advertiserID)
	default:
		return AudiencePage{}, fmt.Errorf("unsupported audience type %q", kind)
	}

	rawURL := c.apiURL(path) + "?" + q.Encode()
	items, next, err := c.listOnePage(ctx, at, rawURL, key)
	if err != nil {
		return AudiencePage{}, err
	}

	out := AudiencePage{Audiences: make([]Audience, 0, len(items)), NextPageToken: next}
	for _, raw := range items {
		var row struct {
			ID               string `json:"firstPartyAndPartnerAudienceId"`
			GoogleAudienceID string `json:"googleAudienceId"`
			CombinedID       string `json:"combinedAudienceId"`
			CustomListID     string `json:"customListId"`
			LookalikeID      string `json:"lookalikeAudienceId"`
			DisplayName      string `json:"displayName"`
			Type             string `json:"googleAudienceType"`
			Size             string `json:"displayAudienceSize"`
		}
		if err := json.Unmarshal(raw, &row); err != nil {
			return AudiencePage{}, err
		}
		id := firstNonEmpty(row.ID, row.GoogleAudienceID, row.CombinedID, row.CustomListID, row.LookalikeID)
		out.Audiences = append(out.Audiences, Audience{
			ID:   id,
			Name: row.DisplayName,
			Type: row.Type,
			Size: row.Size,
		})
	}
	return out, nil
}

// LookalikeAudience is a DV360 lookalike audience.
type LookalikeAudience struct {
	ID              string   `json:"lookalikeAudienceId"`
	DisplayName     string   `json:"displayName"`
	ReachLevel      string   `json:"reachLevel"`
	SeedAudienceIDs []string `json:"seedAudienceListIds"`
	GeographyCodes  []string `json:"geographyCodes"`
}

// CreateLookalikeAudience builds a lookalike from existing seed audiences.
func (c *Client) CreateLookalikeAudience(ctx context.Context, refreshToken, advertiserID, name, reachLevel string, seedAudienceIDs, geographyCodes []string) (LookalikeAudience, error) {
	at, err := c.AccessToken(ctx, refreshToken)
	if err != nil {
		return LookalikeAudience{}, err
	}
	if name == "" {
		return LookalikeAudience{}, fmt.Errorf("display name is required")
	}
	if len(seedAudienceIDs) == 0 {
		return LookalikeAudience{}, fmt.Errorf("at least one seed audience id is required")
	}
	if len(geographyCodes) == 0 {
		return LookalikeAudience{}, fmt.Errorf("at least one geography code is required")
	}
	if reachLevel == "" {
		reachLevel = "MEDIUM_REACH"
	}

	body := map[string]any{
		"displayName":         name,
		"reachLevel":          reachLevel,
		"seedAudienceListIds": seedAudienceIDs,
		"geographyCodes":      geographyCodes,
	}
	q := url.Values{}
	q.Set("advertiserId", advertiserID)
	rawURL := c.apiURL("/lookalikeAudiences") + "?" + q.Encode()

	var out LookalikeAudience
	if err := c.postJSON(ctx, at, rawURL, body, &out); err != nil {
		return LookalikeAudience{}, fmt.Errorf("create dv360 lookalike audience: %w", err)
	}
	return out, nil
}

// listOnePage fetches one page of a list endpoint and returns the next token.
func (c *Client) listOnePage(ctx context.Context, accessToken, rawURL, key string) ([]json.RawMessage, string, error) {
	var wrapper map[string]json.RawMessage
	if err := c.getJSON(ctx, accessToken, rawURL, &wrapper); err != nil {
		return nil, "", err
	}
	var items []json.RawMessage
	if raw, ok := wrapper[key]; ok {
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, "", fmt.Errorf("decode %s: %w", key, err)
		}
	}
	next := ""
	if raw, ok := wrapper["nextPageToken"]; ok {
		_ = json.Unmarshal(raw, &next)
	}
	return items, next, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
