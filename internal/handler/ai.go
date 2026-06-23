package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"regexp"
	"strings"

	"github.com/awomore/Pill4rsBE/internal/ai"
	"github.com/awomore/Pill4rsBE/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"
)

type AIHandler struct {
	queries  *db.Queries
	aiClient *ai.Client
}

func NewAIHandler(queries *db.Queries, aiClient *ai.Client) *AIHandler {
	return &AIHandler{queries: queries, aiClient: aiClient}
}

type chatRequest struct {
	ConversationID *string `json:"conversationId"`
	Mode           string  `json:"mode"`
	Message        string  `json:"message"`
}

func (h *AIHandler) Chat(c echo.Context) error {
	userID := c.Get("user_id").(string)
	workspaceID := c.Get("workspace_id").(string)

	var req chatRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	if req.Message == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "message is required"})
	}
	mode := req.Mode
	if mode == "" {
		mode = "copilot"
	}

	uid, err := uuid.Parse(userID)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid user id"})
	}
	wid, err := uuid.Parse(workspaceID)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid workspace id"})
	}

	pgUID := pgtype.UUID{Bytes: uid, Valid: true}
	pgWID := pgtype.UUID{Bytes: wid, Valid: true}
	ctx := c.Request().Context()

	conversationID, err := h.resolveConversation(ctx, req.ConversationID, pgWID, mode)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create conversation"})
	}

	_, err = h.queries.CreateMessage(ctx, db.CreateMessageParams{
		ConversationID: conversationID,
		Role:           "user",
		Content:        req.Message,
	})
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to save message"})
	}

	systemPrompt := "You are a helpful marketing analytics assistant. Be concise and helpful."
	if mode == "onboarding" {
		ws, err := h.queries.GetWorkspaceByUserID(ctx, pgUID)
		if err == nil {
			systemPrompt = ai.BuildOnboardingPrompt(ws)
		}
	}

	history := h.loadHistory(ctx, conversationID)

	stream, err := h.aiClient.StreamChat(ctx, systemPrompt, history)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "ai request failed"})
	}

	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")
	c.Response().WriteHeader(http.StatusOK)

	var fullText strings.Builder
	var clientBuf strings.Builder
	inFence := false

	for evt := range stream {
		if evt.Type == "text" {
			fullText.WriteString(evt.Text)
			clientBuf.WriteString(evt.Text)
			inFence = stripFencedBlocks(c, &clientBuf, inFence)
		}
	}

	if clientBuf.Len() > 0 {
		s := clientBuf.String()
		if inFence {
			if idx := strings.Index(s, "\n```"); idx != -1 {
				s = s[idx+4:]
			} else {
				s = ""
			}
		}
		if s != "" {
			writeSSE(c, map[string]string{"text": s})
		}
	}

	assistantText := fullText.String()
	onboardingComplete := false

	if mode == "onboarding" {
		onboardingComplete = h.applyOnboardingBlocks(ctx, pgUID, assistantText)
		if onboardingComplete {
			fmt.Fprintf(c.Response(), "data: {\"onboarding_complete\":true}\n\n")
			c.Response().Flush()
		}
	}

	_, err = h.queries.CreateMessage(ctx, db.CreateMessageParams{
		ConversationID: conversationID,
		Role:           "assistant",
		Content:        assistantText,
	})
	if err != nil {
		fmt.Fprintf(c.Response(), "data: {\"error\":\"failed to save assistant message\"}\n\n")
		c.Response().Flush()
	}

	cidStr := formatUUID(conversationID)
	meta, _ := json.Marshal(map[string]interface{}{
		"conversationId":      cidStr,
		"onboarding_complete": onboardingComplete,
	})
	fmt.Fprintf(c.Response(), "data: %s\n\n", meta)
	fmt.Fprintf(c.Response(), "data: [DONE]\n\n")
	c.Response().Flush()
	return nil
}

func (h *AIHandler) resolveConversation(ctx context.Context, convID *string, workspaceID pgtype.UUID, mode string) (pgtype.UUID, error) {
	if convID != nil {
		cid, err := uuid.Parse(*convID)
		if err != nil {
			return pgtype.UUID{}, err
		}
		return pgtype.UUID{Bytes: cid, Valid: true}, nil
	}
	conv, err := h.queries.CreateConversation(ctx, db.CreateConversationParams{
		WorkspaceID: workspaceID,
		Mode:        mode,
	})
	if err != nil {
		return pgtype.UUID{}, err
	}
	return conv.ID, nil
}

func (h *AIHandler) loadHistory(ctx context.Context, conversationID pgtype.UUID) []ai.Message {
	msgs, err := h.queries.GetMessagesByConversation(ctx, db.GetMessagesByConversationParams{
		ConversationID: conversationID,
		Limit:          50,
	})
	if err != nil {
		return nil
	}
	history := make([]ai.Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == "system" {
			continue
		}
		history = append(history, ai.Message{Role: m.Role, Content: m.Content})
	}
	return history
}

var jsonBlockRe = regexp.MustCompile("```json\\s*([\\s\\S]*?)\\s*```")

func (h *AIHandler) applyOnboardingBlocks(ctx context.Context, userID pgtype.UUID, text string) bool {
	blocks := extractJSONBlocks(text)
	if len(blocks) == 0 {
		return false
	}

	ws, err := h.queries.GetWorkspaceByUserID(ctx, userID)
	if err != nil {
		return false
	}

	params := db.UpdateWorkspaceProfileParams{
		ID:             ws.ID,
		BusinessName:   ws.BusinessName,
		Industry:       ws.Industry,
		MonthlyBudget:  ws.MonthlyBudget,
		PrimaryGoal:    ws.PrimaryGoal,
		TargetAudience: ws.TargetAudience,
	}

	completed := false
	for _, raw := range blocks {
		var parsed map[string]interface{}
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
			continue
		}

		if c, ok := parsed["onboarding_complete"]; ok {
			if b, ok := c.(bool); ok && b {
				completed = true
			}
			continue
		}

		field, _ := parsed["field"].(string)
		value, _ := parsed["value"].(string)
		if field == "" || value == "" {
			continue
		}

		switch field {
		case "business_name":
			params.BusinessName = pgtype.Text{String: value, Valid: true}
		case "industry":
			params.Industry = pgtype.Text{String: value, Valid: true}
		case "monthly_budget":
			n := new(big.Int)
			if _, ok := n.SetString(value, 10); ok {
				params.MonthlyBudget = pgtype.Numeric{Int: n, Exp: 0, Valid: true}
			}
		case "primary_goal":
			params.PrimaryGoal = pgtype.Text{String: value, Valid: true}
		case "target_audience":
			params.TargetAudience = pgtype.Text{String: value, Valid: true}
		}
	}

	_, err = h.queries.UpdateWorkspaceProfile(ctx, params)
	if err != nil {
		return false
	}

	if completed {
		h.queries.SetOnboardingComplete(ctx, ws.ID)
	}

	return completed
}

func extractJSONBlocks(text string) []string {
	var blocks []string
	matches := jsonBlockRe.FindAllStringSubmatch(text, -1)
	for _, m := range matches {
		if len(m) > 1 {
			blocks = append(blocks, strings.TrimSpace(m[1]))
		}
	}
	return blocks
}

func writeSSE(c echo.Context, v interface{}) {
	data, _ := json.Marshal(v)
	fmt.Fprintf(c.Response(), "data: %s\n\n", data)
	c.Response().Flush()
}

func stripFencedBlocks(c echo.Context, buf *strings.Builder, inFence bool) bool {
	for {
		s := buf.String()
		if !inFence {
			idx := fenceStart(s)
			if idx < 0 {
				break
			}
			if idx > 0 {
				writeSSE(c, map[string]string{"text": s[:idx]})
			}
			buf.Reset()
			buf.WriteString(s[idx+len("```json"):])
			inFence = true
			continue
		}
		end := fenceEnd(s)
		if end < 0 {
			break
		}
		buf.Reset()
		buf.WriteString(s[end:])
		inFence = false
	}
	return inFence
}

func fenceStart(s string) int {
	idx := strings.Index(s, "```json")
	if idx < 0 {
		idx = strings.Index(s, "\n```json")
		if idx >= 0 {
			return idx
		}
		idx = strings.Index(s, "```json\n")
	}
	return idx
}

func fenceEnd(s string) int {
	return strings.Index(s, "\n```")
}
