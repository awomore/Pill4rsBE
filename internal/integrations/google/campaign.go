package google

import (
	"context"
	"fmt"
	"strings"

	"github.com/awomore/Pill4rsBE/internal/integrations"
)

var (
	_ integrations.CampaignCreator = (*Client)(nil)
	_ integrations.CampaignAdapter = (*Client)(nil)
)

// EnsureDeliverable builds (or resumes) the Google Ads tree:
// campaign budget -> campaign -> ad group -> ad group ad (responsive search ad),
// all paused. Idempotent on `have`; returns the best-known state on error.
//
// NOTE: this targets SEARCH campaigns with manual CPC and a responsive search
// ad. Google enforces strict RSA rules (>=3 headlines <=30 chars, >=2
// descriptions <=90 chars) and requires an approved developer token. Validate
// the full bodies against a Google Ads test account.
func (c *Client) EnsureDeliverable(ctx context.Context, accessToken string, account integrations.PlatformAccount, spec integrations.CampaignSpec, have integrations.DeliverableState) (integrations.DeliverableState, error) {
	state := have

	at, err := c.accessToken(ctx, accessToken)
	if err != nil {
		return state, err
	}
	cid := account.AccountID
	base := fmt.Sprintf("/customers/%s", cid)

	if state.CampaignID == "" {
		budgetRN, err := c.mutateOne(ctx, at, base+"/campaignBudgets:mutate", map[string]any{
			"name":           spec.Name + " budget",
			"amountMicros":   microAmount(spec.DailyBudget),
			"deliveryMethod": "STANDARD",
		})
		if err != nil {
			return state, err
		}
		campaignRN, err := c.mutateOne(ctx, at, base+"/campaigns:mutate", map[string]any{
			"name":                   spec.Name,
			"status":                 "PAUSED",
			"advertisingChannelType": "SEARCH",
			"campaignBudget":         budgetRN,
			"manualCpc":              map[string]any{},
		})
		if err != nil {
			return state, err
		}
		state.CampaignID = lastSegment(campaignRN)
	}

	if state.AdSetID == "" {
		campaignRN := fmt.Sprintf("customers/%s/campaigns/%s", cid, state.CampaignID)
		adGroupRN, err := c.mutateOne(ctx, at, base+"/adGroups:mutate", map[string]any{
			"name":         spec.Name + " - ad group",
			"campaign":     campaignRN,
			"status":       "PAUSED",
			"type":         "SEARCH_STANDARD",
			"cpcBidMicros": "1000000",
		})
		if err != nil {
			return state, err
		}
		state.AdSetID = lastSegment(adGroupRN)
	}

	if state.AdID == "" {
		if spec.Creative == nil || spec.Creative.LinkURL == "" {
			return state, fmt.Errorf("creative with a destination link is required")
		}
		adGroupRN := fmt.Sprintf("customers/%s/adGroups/%s", cid, state.AdSetID)
		adRN, err := c.mutateOne(ctx, at, base+"/adGroupAds:mutate", map[string]any{
			"adGroup": adGroupRN,
			"status":  "PAUSED",
			"ad": map[string]any{
				"finalUrls": []string{spec.Creative.LinkURL},
				"responsiveSearchAd": map[string]any{
					"headlines":    rsaHeadlines(spec),
					"descriptions": rsaDescriptions(spec),
				},
			},
		})
		if err != nil {
			return state, err
		}
		state.AdID = lastSegment(adRN)
	}

	return state, nil
}

func rsaHeadlines(spec integrations.CampaignSpec) []map[string]string {
	var cands []string
	if spec.Creative != nil && spec.Creative.Headline != "" {
		cands = append(cands, spec.Creative.Headline)
	}
	cands = append(cands, spec.Name, "Learn More", "Shop Now")
	return textObjs(cands, 3, 30)
}

func rsaDescriptions(spec integrations.CampaignSpec) []map[string]string {
	var cands []string
	if spec.Creative != nil {
		if spec.Creative.PrimaryText != "" {
			cands = append(cands, spec.Creative.PrimaryText)
		}
		if spec.Creative.Description != "" {
			cands = append(cands, spec.Creative.Description)
		}
	}
	cands = append(cands, "Discover more today.", "Visit our site to learn more.")
	return textObjs(cands, 2, 90)
}

func textObjs(cands []string, n, max int) []map[string]string {
	out := make([]map[string]string, 0, n)
	for _, s := range cands {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if r := []rune(s); len(r) > max {
			s = string(r[:max])
		}
		out = append(out, map[string]string{"text": s})
		if len(out) >= n {
			break
		}
	}
	return out
}
