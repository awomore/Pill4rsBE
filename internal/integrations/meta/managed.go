package meta

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// CreateManagedAdAccount creates an ad account under the Pill4rs Business
// Manager using a system-user access token.
func (c *Client) CreateManagedAdAccount(ctx context.Context, accessToken, businessID, name, currency string, timezoneID int) (string, error) {
	form := url.Values{}
	form.Set("name", strings.TrimSpace(name))
	form.Set("currency", strings.ToUpper(strings.TrimSpace(currency)))
	form.Set("timezone_id", strconv.Itoa(timezoneID))
	form.Set("access_token", accessToken)

	endpoint := fmt.Sprintf("%s/%s/%s/adaccounts", c.graphBaseURL, c.apiVersion, businessID)
	return c.postForm(ctx, endpoint, form)
}

// SetSpendCap updates an ad account's hard spend cap in minor currency units.
func (c *Client) SetSpendCap(ctx context.Context, accessToken, accountID string, spendCapMinor int64) error {
	form := url.Values{}
	form.Set("spend_cap", strconv.FormatInt(spendCapMinor, 10))
	form.Set("access_token", accessToken)

	endpoint := fmt.Sprintf("%s/%s/%s", c.graphBaseURL, c.apiVersion, ensureActPrefix(accountID))
	return c.postFormNoID(ctx, endpoint, form)
}

func (c *Client) postFormNoID(ctx context.Context, endpoint string, form url.Values) error {
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
		Success bool        `json:"success"`
		Error   *graphError `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if parsed.Error != nil {
		return parsed.Error
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("request failed: status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}
