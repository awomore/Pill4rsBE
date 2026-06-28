package google

import (
	"context"
	"fmt"

	"github.com/awomore/Pill4rsBE/internal/integrations"
)

var _ integrations.CampaignManager = (*Client)(nil)

// SetStatus pauses/resumes a campaign (ACTIVE -> ENABLED, else PAUSED).
func (c *Client) SetStatus(ctx context.Context, accessToken, accountID, externalCampaignID, status string) error {
	at, err := c.accessToken(ctx, accessToken)
	if err != nil {
		return err
	}
	googleStatus := "PAUSED"
	if status == "ACTIVE" {
		googleStatus = "ENABLED"
	}
	resourceName := fmt.Sprintf("customers/%s/campaigns/%s", accountID, externalCampaignID)
	body := map[string]any{
		"operations": []any{map[string]any{
			"update":     map[string]any{"resourceName": resourceName, "status": googleStatus},
			"updateMask": "status",
		}},
	}
	_, err = c.adsPost(ctx, at, fmt.Sprintf("/customers/%s/campaigns:mutate", accountID), body)
	return err
}

// UpdateAdSetBudget is not supported on Google Ads via the ad-group id: budgets
// live on a separate campaign-budget resource. (A future slice can resolve and
// mutate the campaign's budget resource.)
func (c *Client) UpdateAdSetBudget(ctx context.Context, accessToken, accountID, externalAdSetID string, dailyBudget float64) error {
	return fmt.Errorf("google ads budget changes are managed at the campaign-budget level and are not supported via this action yet")
}
