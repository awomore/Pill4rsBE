package meta

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/awomore/Pill4rsBE/internal/integrations"
)

var _ integrations.CampaignManager = (*Client)(nil)

// SetStatus flips a Meta campaign between ACTIVE and PAUSED. accountID is unused
// (Meta campaign nodes are global ids), but kept for the cross-platform contract.
func (c *Client) SetStatus(ctx context.Context, accessToken, accountID, externalCampaignID, status string) error {
	form := url.Values{}
	form.Set("status", status)
	form.Set("access_token", accessToken)
	endpoint := fmt.Sprintf("%s/%s/%s", c.graphBaseURL, c.apiVersion, externalCampaignID)
	return c.postUpdate(ctx, endpoint, form)
}

// UpdateAdSetBudget sets the daily budget (major units -> minor) on an ad set.
func (c *Client) UpdateAdSetBudget(ctx context.Context, accessToken, accountID, externalAdSetID string, dailyBudget float64) error {
	form := url.Values{}
	form.Set("daily_budget", minorUnits(dailyBudget))
	form.Set("access_token", accessToken)
	endpoint := fmt.Sprintf("%s/%s/%s", c.graphBaseURL, c.apiVersion, externalAdSetID)
	return c.postUpdate(ctx, endpoint, form)
}

// postUpdate POSTs to a Graph node edge that returns {"success": true} (no id).
func (c *Client) postUpdate(ctx context.Context, endpoint string, form url.Values) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	var parsed struct {
		Error *graphError `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if parsed.Error != nil {
		return parsed.Error
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("update failed: status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}
