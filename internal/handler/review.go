package handler

import (
	"log/slog"
	"net/http"

	"github.com/awomore/Pill4rsBE/internal/service"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// ReviewHandler runs Oma's workspace-wide campaign review across every platform
// and turns the findings into reviewable actions.
type ReviewHandler struct {
	campaigns *service.CampaignService
	actions   *service.ActionService
}

func NewReviewHandler(campaigns *service.CampaignService, actions *service.ActionService) *ReviewHandler {
	return &ReviewHandler{campaigns: campaigns, actions: actions}
}

type reviewRequest struct {
	Propose   *bool `json:"propose"`
	AutoApply bool  `json:"auto_apply"`
}

// Run reviews every campaign and, unless disabled, queues Oma's recommended
// actions. With auto_apply it executes them immediately (Oma in full control);
// otherwise they wait in the approval queue.
func (h *ReviewHandler) Run(c echo.Context) error {
	wid, err := uuid.Parse(workspaceIDOf(c))
	if err != nil {
		return badRequest(c, "invalid workspace id")
	}

	var req reviewRequest
	_ = c.Bind(&req)
	propose := true
	if req.Propose != nil {
		propose = *req.Propose
	}

	ctx := c.Request().Context()
	review, err := h.campaigns.ReviewWorkspace(ctx, wid)
	if err != nil {
		slog.Error("workspace review failed", "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to review campaigns"})
	}

	resp := map[string]interface{}{"review": review}
	if !propose {
		return c.JSON(http.StatusOK, resp)
	}

	created, err := h.actions.ProposeFromReview(ctx, wid, review)
	if err != nil {
		slog.Error("propose from review failed", "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to queue review actions"})
	}

	if !req.AutoApply {
		proposals := make([]map[string]interface{}, 0, len(created))
		for _, a := range created {
			proposals = append(proposals, actionToMap(a))
		}
		resp["proposed"] = proposals
		return c.JSON(http.StatusOK, resp)
	}

	executed := make([]map[string]interface{}, 0, len(created))
	for _, a := range created {
		updated, _, aerr := h.actions.ApproveAction(ctx, wid, uuid.UUID(a.ID.Bytes))
		if aerr != nil {
			slog.Error("auto-apply review action failed", "action", formatUUID(a.ID), "error", aerr)
			executed = append(executed, map[string]interface{}{
				"id":    formatUUID(a.ID),
				"error": aerr.Error(),
			})
			continue
		}
		executed = append(executed, actionToMap(updated))
	}
	resp["executed"] = executed
	return c.JSON(http.StatusOK, resp)
}
