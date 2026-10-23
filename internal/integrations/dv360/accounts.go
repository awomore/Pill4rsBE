package dv360

import (
	"context"
	"fmt"
	"net/url"

	"github.com/awomore/Pill4rsBE/internal/integrations"
)

// Partner is a DV360 partner (the top-level container for advertisers).
type Partner struct {
	PartnerID   string `json:"partnerId"`
	DisplayName string `json:"displayName"`
}

// Advertiser is a DV360 advertiser. Each advertiser maps to one Pill4rs
// ad_account, and all campaign operations are advertiser-scoped.
type Advertiser struct {
	AdvertiserID string `json:"advertiserId"`
	DisplayName  string `json:"displayName"`
	PartnerID    string `json:"partnerId"`
}

// ListPartners returns every partner accessible to the connected Google user.
func (c *Client) ListPartners(ctx context.Context, refreshToken string) ([]Partner, error) {
	at, err := c.AccessToken(ctx, refreshToken)
	if err != nil {
		return nil, err
	}
	return c.listPartners(ctx, at)
}

func (c *Client) listPartners(ctx context.Context, accessToken string) ([]Partner, error) {
	items, err := c.listAll(ctx, accessToken, c.apiURL("/partners"), "partners")
	if err != nil {
		return nil, err
	}
	return decodeAll[Partner](items)
}

// ListAdvertisers returns the advertisers under one partner.
func (c *Client) ListAdvertisers(ctx context.Context, refreshToken, partnerID string) ([]Advertiser, error) {
	at, err := c.AccessToken(ctx, refreshToken)
	if err != nil {
		return nil, err
	}
	return c.listAdvertisers(ctx, at, partnerID)
}

func (c *Client) listAdvertisers(ctx context.Context, accessToken, partnerID string) ([]Advertiser, error) {
	q := url.Values{}
	q.Set("partnerId", partnerID)
	items, err := c.listAll(ctx, accessToken, c.apiURL("/advertisers")+"?"+q.Encode(), "advertisers")
	if err != nil {
		return nil, err
	}
	ads, err := decodeAll[Advertiser](items)
	if err != nil {
		return nil, err
	}
	for i := range ads {
		if ads[i].PartnerID == "" {
			ads[i].PartnerID = partnerID
		}
	}
	return ads, nil
}

// FetchAdvertisers lists every advertiser across every accessible partner. It
// is used by the connect/callback flow and by re-sync.
func (c *Client) FetchAdvertisers(ctx context.Context, refreshToken string) ([]integrations.Account, error) {
	at, err := c.AccessToken(ctx, refreshToken)
	if err != nil {
		return nil, err
	}
	partners, err := c.listPartners(ctx, at)
	if err != nil {
		return nil, err
	}

	var out []integrations.Account
	seen := make(map[string]struct{})
	for _, p := range partners {
		ads, err := c.listAdvertisers(ctx, at, p.PartnerID)
		if err != nil {
			return nil, fmt.Errorf("list advertisers for partner %s: %w", p.PartnerID, err)
		}
		for _, a := range ads {
			if a.AdvertiserID == "" {
				continue
			}
			if _, ok := seen[a.AdvertiserID]; ok {
				continue
			}
			seen[a.AdvertiserID] = struct{}{}
			name := a.DisplayName
			if name == "" {
				name = "Advertiser " + a.AdvertiserID
			}
			out = append(out, integrations.Account{ID: a.AdvertiserID, Name: name})
		}
	}
	return out, nil
}

// CreateAdvertiser creates a new advertiser under the Pill4rs DSP partner.
func (c *Client) CreateAdvertiser(ctx context.Context, refreshToken, partnerID, billingProfileID, name, domainURL, currencyCode string) (string, error) {
	at, err := c.AccessToken(ctx, refreshToken)
	if err != nil {
		return "", err
	}
	body := map[string]any{
		"displayName":  name,
		"partnerId":    partnerID,
		"entityStatus": "ENTITY_STATUS_ACTIVE",
		"generalConfig": map[string]any{
			"domainUrl":    domainURL,
			"currencyCode": currencyCode,
		},
		"adServerConfig": map[string]any{
			"thirdPartyOnlyConfig": map[string]any{},
		},
		"creativeConfig": map[string]any{},
		"billingConfig": map[string]any{
			"billingProfileId": billingProfileID,
		},
		"containsEuPoliticalAds": "DOES_NOT_CONTAIN_EU_POLITICAL_ADVERTISING",
	}
	var out struct {
		AdvertiserID string `json:"advertiserId"`
	}
	if err := c.postJSON(ctx, at, c.apiURL("/advertisers"), body, &out); err != nil {
		return "", fmt.Errorf("create dv360 advertiser: %w", err)
	}
	if out.AdvertiserID == "" {
		return "", fmt.Errorf("create dv360 advertiser returned no advertiserId")
	}
	return out.AdvertiserID, nil
}
