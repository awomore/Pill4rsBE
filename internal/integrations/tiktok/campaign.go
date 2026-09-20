package tiktok

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/awomore/Pill4rsBE/internal/integrations"
)

var (
	_ integrations.CampaignCreator = (*Client)(nil)
	_ integrations.CampaignAdapter = (*Client)(nil)
)

// objectiveMap translates internal objectives to TikTok objective_type values.
var objectiveMap = map[integrations.CampaignObjective]string{
	integrations.ObjectiveAwareness:    "REACH",
	integrations.ObjectiveTraffic:      "TRAFFIC",
	integrations.ObjectiveEngagement:   "ENGAGEMENT",
	integrations.ObjectiveLeads:        "LEAD_GENERATION",
	integrations.ObjectiveSales:        "WEB_CONVERSIONS",
	integrations.ObjectiveAppPromotion: "APP_PROMOTION",
}

// EnsureDeliverable builds (or resumes) the TikTok tree: campaign -> ad group ->
// image (uploaded by URL) -> ad, all paused. Idempotent on `have`, returns the
// best-known state on error so the caller can persist progress and resume.
//
// NOTE: TikTok ad groups require many objective-specific fields (placement,
// location, optimization_goal, billing_event, bid, schedule). The bodies below
// are a sensible baseline; validate the full field set against a TikTok sandbox.
func (c *Client) EnsureDeliverable(ctx context.Context, accessToken string, account integrations.PlatformAccount, spec integrations.CampaignSpec, have integrations.DeliverableState) (integrations.DeliverableState, error) {
	state := have

	objective, ok := objectiveMap[spec.Objective]
	if !ok {
		return state, fmt.Errorf("objective %q is not supported by tiktok", spec.Objective)
	}

	if state.CampaignID == "" {
		data, err := c.post(ctx, "/campaign/create/", accessToken, map[string]any{
			"advertiser_id":    account.AccountID,
			"campaign_name":    spec.Name,
			"objective_type":   objective,
			"budget_mode":      "BUDGET_MODE_DAY",
			"budget":           spec.DailyBudget,
			"operation_status": "DISABLE",
		})
		if err != nil {
			return state, err
		}
		var out struct {
			CampaignID string `json:"campaign_id"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return state, fmt.Errorf("decode campaign: %w", err)
		}
		state.CampaignID = out.CampaignID
	}

	if state.AdSetID == "" {
		data, err := c.post(ctx, "/adgroup/create/", accessToken, map[string]any{
			"advertiser_id":     account.AccountID,
			"campaign_id":       state.CampaignID,
			"adgroup_name":      spec.Name + " - ad group",
			"budget_mode":       "BUDGET_MODE_DAY",
			"budget":            spec.DailyBudget,
			"placement_type":    "PLACEMENT_TYPE_AUTOMATIC",
			"optimization_goal": "CLICK",
			"billing_event":     "CPC",
			"bid_type":          "BID_TYPE_NO_BID",
			"schedule_type":     "SCHEDULE_FROM_NOW",
			"operation_status":  "DISABLE",
		})
		if err != nil {
			return state, err
		}
		var out struct {
			AdgroupID string `json:"adgroup_id"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return state, fmt.Errorf("decode ad group: %w", err)
		}
		state.AdSetID = out.AdgroupID
	}

	if state.CreativeID == "" {
		cr := spec.FirstVariantCreative()
		if cr == nil {
			return state, fmt.Errorf("creative media is required")
		}
		if cr.Format == "video" && cr.VideoURL != "" {
			data, err := c.post(ctx, "/file/video/ad/upload/", accessToken, map[string]any{
				"advertiser_id": account.AccountID,
				"upload_type":   "UPLOAD_BY_URL",
				"video_url":     cr.VideoURL,
			})
			if err != nil {
				return state, err
			}
			var out struct {
				VideoID string `json:"video_id"`
			}
			if err := json.Unmarshal(data, &out); err != nil {
				return state, fmt.Errorf("decode video: %w", err)
			}
			if out.VideoID == "" {
				return state, fmt.Errorf("video upload returned no video id")
			}
			state.CreativeID = out.VideoID
		} else {
			if cr.ImageURL == "" {
				return state, fmt.Errorf("creative image is required")
			}
			data, err := c.post(ctx, "/file/image/ad/upload/", accessToken, map[string]any{
				"advertiser_id": account.AccountID,
				"upload_type":   "UPLOAD_BY_URL",
				"image_url":     cr.ImageURL,
			})
			if err != nil {
				return state, err
			}
			var out struct {
				ImageID string `json:"image_id"`
			}
			if err := json.Unmarshal(data, &out); err != nil {
				return state, fmt.Errorf("decode image: %w", err)
			}
			state.CreativeID = out.ImageID
		}
	}

	if state.AdID == "" {
		cr := spec.FirstVariantCreative()
		creative := map[string]any{
			"ad_name":          spec.Name + " - ad",
			"ad_format":        adFormat(spec),
			"ad_text":          creativeText(spec),
			"call_to_action":   ctaOrDefault(spec.CTA),
			"landing_page_url": creativeLink(spec),
		}
		if cr != nil && cr.Format == "video" {
			creative["video_id"] = state.CreativeID
		} else {
			creative["image_ids"] = []string{state.CreativeID}
		}
		data, err := c.post(ctx, "/ad/create/", accessToken, map[string]any{
			"advertiser_id":    account.AccountID,
			"adgroup_id":       state.AdSetID,
			"creatives":        []any{creative},
			"operation_status": "DISABLE",
		})
		if err != nil {
			return state, err
		}
		var out struct {
			AdIDs []string `json:"ad_ids"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return state, fmt.Errorf("decode ad: %w", err)
		}
		if len(out.AdIDs) == 0 {
			return state, fmt.Errorf("ad create returned no ad id")
		}
		state.AdID = out.AdIDs[0]
	}

	return state, nil
}

func creativeText(spec integrations.CampaignSpec) string {
	cr := spec.FirstVariantCreative()
	if cr == nil {
		return spec.Name
	}
	if cr.PrimaryText != "" {
		return cr.PrimaryText
	}
	if cr.Headline != "" {
		return cr.Headline
	}
	return spec.Name
}

func creativeLink(spec integrations.CampaignSpec) string {
	cr := spec.FirstVariantCreative()
	if cr == nil {
		return ""
	}
	return cr.LinkURL
}

func ctaOrDefault(cta string) string {
	if cta == "" {
		return "LEARN_MORE"
	}
	return cta
}

// adFormat maps the normalized creative format to TikTok's ad_format value.
// Images and videos are both uploaded by URL before ad creation.
func adFormat(spec integrations.CampaignSpec) string {
	cr := spec.FirstVariantCreative()
	if cr != nil && cr.Format == "video" {
		return "SINGLE_VIDEO"
	}
	return "SINGLE_IMAGE"
}
