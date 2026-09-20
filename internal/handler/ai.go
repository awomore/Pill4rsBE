package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/awomore/Pill4rsBE/internal/ai"
	"github.com/awomore/Pill4rsBE/internal/db"
	"github.com/awomore/Pill4rsBE/internal/integrations"
	"github.com/awomore/Pill4rsBE/internal/service"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"
)

type AIHandler struct {
	queries  *db.Queries
	aiClient *ai.Client
	actions  *service.ActionService
	wallet   *service.WalletService
}

func NewAIHandler(queries *db.Queries, aiClient *ai.Client, actions *service.ActionService, wallet *service.WalletService) *AIHandler {
	return &AIHandler{queries: queries, aiClient: aiClient, actions: actions, wallet: wallet}
}

type proposeCampaignRequest struct {
	Message      string   `json:"message"`
	AdAccountIDs []string `json:"ad_account_ids"`
}

// ProposeCampaign turns a natural-language instruction into a campaign proposal
// (a pending action) for the user to review and approve. Oma never executes;
// approval runs it through the same campaign engine the manual flow uses.
func (h *AIHandler) ProposeCampaign(c echo.Context) error {
	userID, _ := c.Get("user_id").(string)
	workspaceID, _ := c.Get("workspace_id").(string)
	uid, err := uuid.Parse(userID)
	if err != nil {
		return badRequest(c, "invalid user id")
	}
	wid, err := uuid.Parse(workspaceID)
	if err != nil {
		return badRequest(c, "invalid workspace id")
	}

	var req proposeCampaignRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "invalid request body")
	}
	if strings.TrimSpace(req.Message) == "" {
		return badRequest(c, "message is required")
	}
	if len(req.AdAccountIDs) == 0 {
		return badRequest(c, "ad_account_ids is required — pick which account(s) to run on")
	}

	ctx := c.Request().Context()
	pgUID := pgtype.UUID{Bytes: uid, Valid: true}
	pgWID := pgtype.UUID{Bytes: wid, Valid: true}

	system := h.buildCopilotPrompt(ctx, pgUID, pgWID, nil) +
		"\n\nThe user wants to create a new ad campaign. Call submit_campaign with a complete, " +
		"sensible spec grounded in their business, budget and goal: pick an objective that matches " +
		"their goal, a daily budget within their means, realistic targeting, and compelling creative " +
		"(primary text, headline, a destination link_url and an image_url). Use YYYY-MM-DD for dates."

	proposed, err := h.aiClient.ProposeCampaign(ctx, system, req.Message)
	if err != nil {
		slog.Error("oma propose campaign failed", "error", err)
		return c.JSON(http.StatusBadGateway, map[string]string{"error": "could not generate a campaign proposal"})
	}

	payload := service.CampaignProposalPayload{
		Name:         proposed.Name,
		Objective:    proposed.Objective,
		DailyBudget:  proposed.DailyBudget,
		Currency:     proposed.Currency,
		StartDate:    proposed.StartDate,
		EndDate:      proposed.EndDate,
		CTA:          proposed.CTA,
		AdAccountIDs: req.AdAccountIDs,
		Targeting:    proposed.Targeting,
		Creative:     creativeFromProposed(proposed.Creative),
		Rationale:    proposed.Rationale,
		Provenance:   omaProvenance(proposed),
	}

	action, err := h.actions.ProposeCreateCampaign(ctx, wid, service.ActorOma, payload)
	if err != nil {
		var ve service.ValidationError
		if errors.As(err, &ve) {
			return badRequest(c, "Oma's proposal was incomplete: "+ve.Msg)
		}
		slog.Error("store proposal failed", "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to save proposal"})
	}
	return c.JSON(http.StatusCreated, actionToMap(action))
}

func creativeFromProposed(cr *ai.ProposedCreative) *integrations.CreativeSpec {
	if cr == nil {
		return nil
	}
	return &integrations.CreativeSpec{
		PrimaryText: cr.PrimaryText,
		Headline:    cr.Headline,
		Description: cr.Description,
		LinkURL:     cr.LinkURL,
		ImageURL:    cr.ImageURL,
		Format:      cr.Format,
	}
}

func omaProvenance(pc *ai.ProposedCampaign) map[string]string {
	p := make(map[string]string)
	if pc.Name != "" {
		p["name"] = "oma"
	}
	if pc.Objective != "" {
		p["objective"] = "oma"
	}
	if pc.DailyBudget > 0 {
		p["daily_budget"] = "oma"
	}
	if pc.Currency != "" {
		p["currency"] = "oma"
	}
	if pc.CTA != "" {
		p["cta"] = "oma"
	}
	if pc.StartDate != "" {
		p["start_date"] = "oma"
	}
	if pc.EndDate != "" {
		p["end_date"] = "oma"
	}
	if len(pc.Targeting) > 0 {
		p["targeting"] = "oma"
		for k := range pc.Targeting {
			p["variants[0].targeting."+k] = "oma"
		}
	}
	if pc.Creative != nil {
		p["creative"] = "oma"
		p["variants[0].creative.primary_text"] = "oma"
		p["variants[0].creative.headline"] = "oma"
		p["variants[0].creative.description"] = "oma"
		p["variants[0].creative.link_url"] = "oma"
		p["variants[0].creative.image_url"] = "oma"
	}
	return p
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
	if strings.TrimSpace(req.Message) == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "message is required"})
	}
	mode := req.Mode
	if mode == "" {
		mode = "copilot"
	}
	if mode != "copilot" && mode != "onboarding" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "mode must be 'copilot' or 'onboarding'"})
	}
	if req.ConversationID != nil {
		if _, err := uuid.Parse(*req.ConversationID); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid conversationId"})
		}
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

	history := h.loadHistory(ctx, conversationID)

	systemPrompt := "You are a helpful marketing analytics assistant. Be concise and helpful."
	switch mode {
	case "onboarding":
		ws, err := h.queries.GetWorkspaceByUserID(ctx, pgUID)
		if err == nil {
			systemPrompt = ai.BuildOnboardingPrompt(ws)
		}
	case "copilot":
		systemPrompt = h.buildCopilotPrompt(ctx, pgUID, pgWID, history)
	}

	stream, err := h.aiClient.StreamChat(ctx, systemPrompt, history)
	if err != nil {
		slog.Error("ai stream failed", "error", err)
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

func (h *AIHandler) buildCopilotPrompt(ctx context.Context, userID, workspaceID pgtype.UUID, recent []ai.Message) string {
	const fallback = "You are Oma, the user's marketing copilot. Give specific, numbers-grounded advice."

	ws, err := h.queries.GetWorkspaceByUserID(ctx, userID)
	if err != nil {
		return fallback
	}

	adAccounts, err := h.queries.GetAdAccountsByWorkspace(ctx, workspaceID)
	if err != nil {
		adAccounts = nil
	}

	end := time.Now().UTC()
	start := end.AddDate(0, 0, -30)
	startDate := pgtype.Date{Time: start, Valid: true}
	endDate := pgtype.Date{Time: end, Valid: true}

	var summary ai.PerformanceSummary
	if totals, err := h.queries.GetDashboardTotals(ctx, db.GetDashboardTotalsParams{
		WorkspaceID: workspaceID, Date: startDate, Date_2: endDate,
	}); err == nil {
		summary.TotalSpend, _ = numericToFloat(totals.TotalSpend)
		summary.TotalImpressions = totals.TotalImpressions
		summary.TotalClicks = totals.TotalClicks
		summary.TotalConversions = totals.TotalConversions
		summary.AverageRoas, _ = numericToFloat(totals.AverageRoas)
		summary.AverageCpc, _ = numericToFloat(totals.AverageCpc)
	}

	// Prepaid wallet context so Oma budgets within available funds.
	summary.SpendState = ws.SpendState
	if h.wallet != nil {
		if wsum, werr := h.wallet.Summary(ctx, uuid.UUID(workspaceID.Bytes)); werr == nil {
			for _, bal := range wsum.Balances {
				summary.Balances = append(summary.Balances, ai.BalanceLine{
					Currency:     bal.Currency,
					BalanceMinor: bal.BalanceMinor,
				})
			}
		}
	}

	if breakdown, err := h.queries.GetDashboardCampaignBreakdown(ctx, db.GetDashboardCampaignBreakdownParams{
		WorkspaceID: workspaceID, Date: startDate, Date_2: endDate,
	}); err == nil {
		for _, bdn := range breakdown {
			spend, _ := numericToFloat(bdn.Spend)
			roas, _ := numericToFloat(bdn.AverageRoas)
			summary.Campaigns = append(summary.Campaigns, ai.CampaignPerformance{
				Name:        bdn.Name,
				Status:      bdn.Status,
				Spend:       spend,
				Roas:        roas,
				Conversions: bdn.Conversions,
			})
		}
	}

	return ai.BuildCopilotPrompt(ws, adAccounts, summary, recent)
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
