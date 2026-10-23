package dv360

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// CustomBiddingAlgorithm is a DV360 script- or rule-based bidding algorithm.
type CustomBiddingAlgorithm struct {
	ID           string `json:"customBiddingAlgorithmId"`
	DisplayName  string `json:"displayName"`
	Type         string `json:"customBiddingAlgorithmType"`
	Status       string `json:"entityStatus"`
	AdvertiserID string `json:"advertiserId"`
}

// CustomBiddingScript is an uploaded algorithm script and its review state.
type CustomBiddingScript struct {
	ID     string `json:"customBiddingScriptId"`
	State  string `json:"state"`
	Active bool   `json:"active"`
}

// CustomBiddingRule is an uploaded algorithm rules resource and its state.
type CustomBiddingRule struct {
	ID     string `json:"customBiddingAlgorithmRulesId"`
	State  string `json:"state"`
	Active bool   `json:"active"`
}

// ListCustomBiddingAlgorithms returns one page of algorithms for an advertiser.
func (c *Client) ListCustomBiddingAlgorithms(ctx context.Context, refreshToken, advertiserID, pageToken string, pageSize int) ([]CustomBiddingAlgorithm, string, error) {
	at, err := c.AccessToken(ctx, refreshToken)
	if err != nil {
		return nil, "", err
	}
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 100
	}
	q := url.Values{}
	q.Set("advertiserId", advertiserID)
	q.Set("pageSize", strconv.Itoa(pageSize))
	if pageToken != "" {
		q.Set("pageToken", pageToken)
	}
	items, next, err := c.listOnePage(ctx, at, c.apiURL("/customBiddingAlgorithms")+"?"+q.Encode(), "customBiddingAlgorithms")
	if err != nil {
		return nil, "", err
	}
	rows, err := decodeAll[CustomBiddingAlgorithm](items)
	return rows, next, err
}

// CreateCustomBiddingAlgorithm creates a script- or rule-based algorithm. The
// script/rules file is uploaded separately.
func (c *Client) CreateCustomBiddingAlgorithm(ctx context.Context, refreshToken, advertiserID, name, algorithmType string) (CustomBiddingAlgorithm, error) {
	at, err := c.AccessToken(ctx, refreshToken)
	if err != nil {
		return CustomBiddingAlgorithm{}, err
	}
	if strings.TrimSpace(name) == "" {
		return CustomBiddingAlgorithm{}, fmt.Errorf("display name is required")
	}
	algoType := strings.ToUpper(strings.TrimSpace(algorithmType))
	switch algoType {
	case "SCRIPT", "SCRIPT_BASED":
		algoType = "SCRIPT_BASED"
	case "RULES", "RULE", "RULE_BASED":
		algoType = "RULE_BASED"
	default:
		return CustomBiddingAlgorithm{}, fmt.Errorf("type must be SCRIPT_BASED or RULE_BASED")
	}

	body := map[string]any{
		"displayName":                name,
		"customBiddingAlgorithmType": algoType,
		"advertiserId":               advertiserID,
		"entityStatus":               "ENTITY_STATUS_ACTIVE",
	}
	var out CustomBiddingAlgorithm
	if err := c.postJSON(ctx, at, c.apiURL("/customBiddingAlgorithms"), body, &out); err != nil {
		return CustomBiddingAlgorithm{}, fmt.Errorf("create dv360 custom bidding algorithm: %w", err)
	}
	return out, nil
}

// ListCustomBiddingScripts returns one page of scripts for an algorithm.
func (c *Client) ListCustomBiddingScripts(ctx context.Context, refreshToken, advertiserID, algorithmID, pageToken string, pageSize int) ([]CustomBiddingScript, string, error) {
	at, err := c.AccessToken(ctx, refreshToken)
	if err != nil {
		return nil, "", err
	}
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 100
	}
	q := url.Values{}
	q.Set("advertiserId", advertiserID)
	q.Set("pageSize", strconv.Itoa(pageSize))
	if pageToken != "" {
		q.Set("pageToken", pageToken)
	}
	path := fmt.Sprintf("/customBiddingAlgorithms/%s/scripts?%s", url.PathEscape(algorithmID), q.Encode())
	items, next, err := c.listOnePage(ctx, at, c.apiURL(path), "customBiddingScripts")
	if err != nil {
		return nil, "", err
	}
	rows, err := decodeAll[CustomBiddingScript](items)
	return rows, next, err
}

// UploadCustomBiddingScript uploads a JavaScript scoring script and creates the
// script resource that DV360 reviews.
func (c *Client) UploadCustomBiddingScript(ctx context.Context, refreshToken, advertiserID, algorithmID, script string) (CustomBiddingScript, error) {
	at, err := c.AccessToken(ctx, refreshToken)
	if err != nil {
		return CustomBiddingScript{}, err
	}
	if strings.TrimSpace(script) == "" {
		return CustomBiddingScript{}, fmt.Errorf("script is required")
	}

	q := url.Values{}
	q.Set("advertiserId", advertiserID)
	refURL := fmt.Sprintf("%s/%s:uploadScript?%s",
		c.apiURL("/customBiddingAlgorithms"), url.PathEscape(algorithmID), q.Encode())
	var ref struct {
		ResourceName string `json:"resourceName"`
	}
	if err := c.getJSON(ctx, at, refURL, &ref); err != nil {
		return CustomBiddingScript{}, fmt.Errorf("get dv360 script upload ref: %w", err)
	}
	if ref.ResourceName == "" {
		return CustomBiddingScript{}, fmt.Errorf("dv360 returned no script upload resource")
	}
	if err := c.uploadRaw(ctx, at, ref.ResourceName, []byte(script), "application/javascript"); err != nil {
		return CustomBiddingScript{}, err
	}

	createURL := fmt.Sprintf("%s/%s/scripts?%s",
		c.apiURL("/customBiddingAlgorithms"), url.PathEscape(algorithmID), q.Encode())
	body := map[string]any{"script": map[string]any{"resourceName": ref.ResourceName}}
	var out CustomBiddingScript
	if err := c.postJSON(ctx, at, createURL, body, &out); err != nil {
		return CustomBiddingScript{}, fmt.Errorf("create dv360 custom bidding script: %w", err)
	}
	return out, nil
}

// ListCustomBiddingRules returns one page of rules resources for an algorithm.
func (c *Client) ListCustomBiddingRules(ctx context.Context, refreshToken, advertiserID, algorithmID, pageToken string, pageSize int) ([]CustomBiddingRule, string, error) {
	at, err := c.AccessToken(ctx, refreshToken)
	if err != nil {
		return nil, "", err
	}
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 100
	}
	q := url.Values{}
	q.Set("advertiserId", advertiserID)
	q.Set("pageSize", strconv.Itoa(pageSize))
	if pageToken != "" {
		q.Set("pageToken", pageToken)
	}
	path := fmt.Sprintf("/customBiddingAlgorithms/%s/rules?%s", url.PathEscape(algorithmID), q.Encode())
	items, next, err := c.listOnePage(ctx, at, c.apiURL(path), "customBiddingRules")
	if err != nil {
		return nil, "", err
	}
	rows, err := decodeAll[CustomBiddingRule](items)
	return rows, next, err
}

// UploadCustomBiddingRules uploads an AlgorithmRules file (JSON) and creates the
// rules resource that DV360 reviews.
func (c *Client) UploadCustomBiddingRules(ctx context.Context, refreshToken, advertiserID, algorithmID string, rules []byte) (CustomBiddingRule, error) {
	at, err := c.AccessToken(ctx, refreshToken)
	if err != nil {
		return CustomBiddingRule{}, err
	}
	if len(bytes.TrimSpace(rules)) == 0 {
		return CustomBiddingRule{}, fmt.Errorf("rules are required")
	}
	if !json.Valid(rules) {
		return CustomBiddingRule{}, fmt.Errorf("rules must be valid JSON")
	}

	q := url.Values{}
	q.Set("advertiserId", advertiserID)
	refURL := fmt.Sprintf("%s/%s:uploadRules?%s",
		c.apiURL("/customBiddingAlgorithms"), url.PathEscape(algorithmID), q.Encode())
	var ref struct {
		ResourceName string `json:"resourceName"`
	}
	if err := c.getJSON(ctx, at, refURL, &ref); err != nil {
		return CustomBiddingRule{}, fmt.Errorf("get dv360 rules upload ref: %w", err)
	}
	if ref.ResourceName == "" {
		return CustomBiddingRule{}, fmt.Errorf("dv360 returned no rules upload resource")
	}
	if err := c.uploadRaw(ctx, at, ref.ResourceName, rules, "application/json"); err != nil {
		return CustomBiddingRule{}, err
	}

	createURL := fmt.Sprintf("%s/%s/rules?%s",
		c.apiURL("/customBiddingAlgorithms"), url.PathEscape(algorithmID), q.Encode())
	body := map[string]any{"rules": map[string]any{"resourceName": ref.ResourceName}}
	var out CustomBiddingRule
	if err := c.postJSON(ctx, at, createURL, body, &out); err != nil {
		return CustomBiddingRule{}, fmt.Errorf("create dv360 custom bidding rules: %w", err)
	}
	return out, nil
}

// uploadRaw performs a Google simple media upload against a resource name
// returned by :uploadScript / :uploadRules.
func (c *Client) uploadRaw(ctx context.Context, accessToken, resourceName string, data []byte, contentType string) error {
	uploadURL := c.apiBaseURL + "/upload/" + apiVersion + "/" +
		strings.TrimPrefix(resourceName, "/") + "?uploadType=media"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("build media upload: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", contentType)
	req.ContentLength = int64(len(data))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("upload dv360 media: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("upload dv360 media: status %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}
