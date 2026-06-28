package tiktok

import (
	"context"

	"github.com/awomore/Pill4rsBE/internal/integrations"
)

var _ integrations.CampaignManager = (*Client)(nil)

// SetStatus enables/disables a TikTok campaign (ACTIVE -> ENABLE, else DISABLE).
func (c *Client) SetStatus(ctx context.Context, accessToken, accountID, externalCampaignID, status string) error {
	op := "DISABLE"
	if status == "ACTIVE" {
		op = "ENABLE"
	}
	_, err := c.post(ctx, "/campaign/status/update/", accessToken, map[string]any{
		"advertiser_id":    accountID,
		"campaign_ids":     []string{externalCampaignID},
		"operation_status": op,
	})
	return err
}

// UpdateAdSetBudget sets an ad group's daily budget (TikTok budget is in major
// currency units).
func (c *Client) UpdateAdSetBudget(ctx context.Context, accessToken, accountID, externalAdSetID string, dailyBudget float64) error {
	_, err := c.post(ctx, "/adgroup/update/", accessToken, map[string]any{
		"advertiser_id": accountID,
		"adgroup_id":    externalAdSetID,
		"budget":        dailyBudget,
	})
	return err
}
