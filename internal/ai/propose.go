package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// ProposedCreative is the creative content Oma fills in for a proposed campaign.
type ProposedCreative struct {
	PrimaryText string `json:"primary_text"`
	Headline    string `json:"headline"`
	Description string `json:"description"`
	LinkURL     string `json:"link_url"`
	ImageURL    string `json:"image_url"`
}

// ProposedCampaign is the structured campaign spec Oma returns via the tool call.
type ProposedCampaign struct {
	Name         string             `json:"name"`
	Objective    string             `json:"objective"`
	DailyBudget  float64            `json:"daily_budget"`
	Currency     string             `json:"currency"`
	StartDate    string             `json:"start_date"`
	EndDate      string             `json:"end_date"`
	CTA          string             `json:"cta"`
	Targeting    map[string]any     `json:"targeting"`
	Creative     *ProposedCreative  `json:"creative"`
	Rationale    map[string]string  `json:"rationale"`
}

// ProposeCampaign asks the model to turn a natural-language instruction into a
// structured campaign spec by forcing a single `submit_campaign` tool call.
func (c *Client) ProposeCampaign(ctx context.Context, systemPrompt, instruction string) (*ProposedCampaign, error) {
	body := map[string]any{
		"model":      "claude-sonnet-4-6",
		"max_tokens": 1024,
		"system":     systemPrompt,
		"messages":   []map[string]any{{"role": "user", "content": instruction}},
		"tools": []any{map[string]any{
			"name":         "submit_campaign",
			"description":  "Propose a complete advertising campaign spec for the user to review and approve.",
			"input_schema": campaignToolSchema(),
		}},
		"tool_choice": map[string]any{"type": "tool", "name": "submit_campaign"},
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/v1/messages", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("anthropic api error %d: %s", resp.StatusCode, string(raw))
	}

	var parsed struct {
		Content []struct {
			Type  string          `json:"type"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	for _, block := range parsed.Content {
		if block.Type == "tool_use" && block.Name == "submit_campaign" {
			var pc ProposedCampaign
			if err := json.Unmarshal(block.Input, &pc); err != nil {
				return nil, fmt.Errorf("decode tool input: %w", err)
			}
			return &pc, nil
		}
	}
	return nil, fmt.Errorf("model did not return a campaign proposal")
}

func campaignToolSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
			"objective": map[string]any{
				"type": "string",
				"enum": []string{"awareness", "traffic", "engagement", "leads", "sales", "app_promotion"},
			},
			"daily_budget": map[string]any{"type": "number", "description": "daily budget in the account's major currency units"},
			"currency":     map[string]any{"type": "string"},
			"start_date":   map[string]any{"type": "string", "description": "YYYY-MM-DD, optional"},
			"end_date":     map[string]any{"type": "string", "description": "YYYY-MM-DD, optional"},
			"cta":          map[string]any{"type": "string", "description": "e.g. SHOP_NOW, LEARN_MORE, SIGN_UP"},
			"targeting": map[string]any{
				"type":        "object",
				"description": "geo (array of ISO country codes), age_min, age_max",
			},
			"creative": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"primary_text": map[string]any{"type": "string"},
					"headline":     map[string]any{"type": "string"},
					"description":  map[string]any{"type": "string"},
					"link_url":     map[string]any{"type": "string"},
					"image_url":    map[string]any{"type": "string"},
				},
			},
			"rationale": map[string]any{
				"type": "object",
				"description": "Short per-field rationale strings explaining why each value was chosen. Keys are field names (name, objective, daily_budget, targeting, creative, cta, start_date, end_date).",
			},
		},
		"required": []string{"name", "objective", "daily_budget", "creative"},
	}
}
