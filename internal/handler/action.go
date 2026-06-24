package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/awomore/Pill4rsBE/internal/db"
	"github.com/awomore/Pill4rsBE/internal/service"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

type ActionHandler struct {
	svc *service.ActionService
}

func NewActionHandler(svc *service.ActionService) *ActionHandler {
	return &ActionHandler{svc: svc}
}

// List returns the workspace's action queue (proposals + their outcomes).
func (h *ActionHandler) List(c echo.Context) error {
	wid, err := uuid.Parse(workspaceIDOf(c))
	if err != nil {
		return badRequest(c, "invalid workspace id")
	}
	actions, err := h.svc.ListActions(c.Request().Context(), wid)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to list actions"})
	}
	out := make([]map[string]interface{}, 0, len(actions))
	for _, a := range actions {
		out = append(out, actionToMap(a))
	}
	return c.JSON(http.StatusOK, out)
}

// Approve executes a pending action through the shared campaign engine.
func (h *ActionHandler) Approve(c echo.Context) error {
	wid, err := uuid.Parse(workspaceIDOf(c))
	if err != nil {
		return badRequest(c, "invalid workspace id")
	}
	aid, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return badRequest(c, "invalid action id")
	}

	action, _, err := h.svc.ApproveAction(c.Request().Context(), wid, aid)
	if err != nil {
		var ve service.ValidationError
		switch {
		case errors.Is(err, service.ErrActionNotFound):
			return c.JSON(http.StatusNotFound, map[string]string{"error": "action not found"})
		case errors.As(err, &ve):
			return badRequest(c, ve.Msg)
		default:
			// Execution failed; the action is recorded as failed.
			slog.Error("approve action failed", "error", err)
			return c.JSON(http.StatusBadGateway, map[string]interface{}{
				"error":  "failed to execute action",
				"action": actionToMap(action),
			})
		}
	}
	return c.JSON(http.StatusOK, actionToMap(action))
}

// Reject closes a pending action without executing it.
func (h *ActionHandler) Reject(c echo.Context) error {
	wid, err := uuid.Parse(workspaceIDOf(c))
	if err != nil {
		return badRequest(c, "invalid workspace id")
	}
	aid, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return badRequest(c, "invalid action id")
	}

	action, err := h.svc.RejectAction(c.Request().Context(), wid, aid)
	if err != nil {
		var ve service.ValidationError
		switch {
		case errors.Is(err, service.ErrActionNotFound):
			return c.JSON(http.StatusNotFound, map[string]string{"error": "action not found"})
		case errors.As(err, &ve):
			return badRequest(c, ve.Msg)
		default:
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to reject action"})
		}
	}
	return c.JSON(http.StatusOK, actionToMap(action))
}

func actionToMap(a db.CampaignAction) map[string]interface{} {
	m := map[string]interface{}{
		"id":     formatUUID(a.ID),
		"actor":  a.Actor,
		"type":   a.Type,
		"status": a.Status,
	}
	if len(a.Payload) > 0 {
		m["payload"] = json.RawMessage(a.Payload)
	}
	if len(a.Result) > 0 {
		m["result"] = json.RawMessage(a.Result)
	}
	if a.Error.Valid {
		m["error"] = a.Error.String
	}
	if a.CreatedAt.Valid {
		m["created_at"] = a.CreatedAt.Time
	}
	if a.UpdatedAt.Valid {
		m["updated_at"] = a.UpdatedAt.Time
	}
	return m
}

func workspaceIDOf(c echo.Context) string {
	id, _ := c.Get("workspace_id").(string)
	return id
}
