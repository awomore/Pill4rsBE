package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type StreamEvent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	Done bool   `json:"done,omitempty"`
}

const defaultAnthropicBaseURL = "https://api.anthropic.com"

type Client struct {
	apiKey     string
	httpClient *http.Client
	baseURL    string
}

func NewClient(apiKey string) *Client {
	return &Client{
		apiKey:     apiKey,
		httpClient: &http.Client{},
		baseURL:    defaultAnthropicBaseURL,
	}
}

func (c *Client) StreamChat(ctx context.Context, systemPrompt string, history []Message) (<-chan StreamEvent, error) {
	messages := make([]map[string]interface{}, 0, len(history))
	for _, m := range history {
		messages = append(messages, map[string]interface{}{
			"role":    m.Role,
			"content": m.Content,
		})
	}

	body := map[string]interface{}{
		"model":      "claude-sonnet-4-6",
		"max_tokens": 4096,
		"system":     systemPrompt,
		"messages":   messages,
		"stream":     true,
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
	req.Header.Set("accept", "text/event-stream")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("anthropic api error %d: %s", resp.StatusCode, string(body))
	}

	ch := make(chan StreamEvent, 64)
	go func() {
		defer resp.Body.Close()
		defer close(ch)
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			payload := strings.TrimPrefix(line, "data: ")
			var event map[string]interface{}
			if err := json.Unmarshal([]byte(payload), &event); err != nil {
				continue
			}
			switch event["type"] {
			case "content_block_delta":
				delta, ok := event["delta"].(map[string]interface{})
				if !ok {
					continue
				}
				if text, ok := delta["text"].(string); ok {
					ch <- StreamEvent{Type: "text", Text: text}
				}
			case "message_stop":
				ch <- StreamEvent{Type: "done", Done: true}
			}
		}
	}()
	return ch, nil
}
