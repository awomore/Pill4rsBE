package tiktok

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// CreateManagedAdvertiser creates an advertiser under the Pill4rs Business
// Center. TikTok requires BC currency/timezone to match the Business Center.
func (c *Client) CreateManagedAdvertiser(ctx context.Context, accessToken, bcID, name, currency, timezone string, industry int, registeredArea, contactEmail, contactName, contactNumber string) (string, error) {
	advertiserInfo := map[string]any{
		"name":     strings.TrimSpace(name),
		"currency": strings.ToUpper(strings.TrimSpace(currency)),
		"timezone": strings.TrimSpace(timezone),
		"type":     "AUCTION",
	}
	customerInfo := map[string]any{"company": strings.TrimSpace(name)}
	if industry > 0 {
		customerInfo["industry"] = industry
	}
	if strings.TrimSpace(registeredArea) != "" {
		customerInfo["registered_area"] = strings.TrimSpace(registeredArea)
	}
	contactInfo := map[string]any{}
	if strings.TrimSpace(contactEmail) != "" {
		contactInfo["email"] = strings.TrimSpace(contactEmail)
	}
	if strings.TrimSpace(contactName) != "" {
		contactInfo["name"] = strings.TrimSpace(contactName)
	}
	if strings.TrimSpace(contactNumber) != "" {
		contactInfo["number"] = strings.TrimSpace(contactNumber)
	}

	body := map[string]any{
		"bc_id":           bcID,
		"advertiser_info": advertiserInfo,
		"customer_info":   customerInfo,
	}
	if len(contactInfo) > 0 {
		body["contact_info"] = contactInfo
	}

	data, err := c.post(ctx, "/bc/advertiser/create/", accessToken, body)
	if err != nil {
		return "", err
	}
	var out struct {
		AdvertiserID  string   `json:"advertiser_id"`
		AdvertiserIDs []string `json:"advertiser_ids"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("decode managed advertiser: %w", err)
	}
	if out.AdvertiserID != "" {
		return out.AdvertiserID, nil
	}
	if len(out.AdvertiserIDs) > 0 {
		return out.AdvertiserIDs[0], nil
	}
	return "", fmt.Errorf("create advertiser returned no advertiser id")
}

// SetAdvertiserBudget sets the account-level budget. budget is in major
// currency units; mode is BUDGET_MODE_TOTAL (default) or BUDGET_MODE_DAY.
func (c *Client) SetAdvertiserBudget(ctx context.Context, accessToken, advertiserID string, budget float64, mode string) error {
	if mode == "" {
		mode = "BUDGET_MODE_TOTAL"
	}
	body := map[string]any{
		"advertiser_id":      advertiserID,
		"budget_update_type": "UPDATE",
		"advertiser_budgets": []any{map[string]any{
			"budget":      budget,
			"budget_mode": mode,
		}},
	}
	_, err := c.post(ctx, "/advertiser/update/", accessToken, body)
	return err
}
