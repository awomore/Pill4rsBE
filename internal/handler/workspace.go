package handler

import (
	"net/http"

	"github.com/awomore/Pill4rsBE/internal/db"
	"github.com/awomore/Pill4rsBE/internal/service"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"
)

type WorkspaceHandler struct {
	svc *service.WorkspaceService
}

func NewWorkspaceHandler(svc *service.WorkspaceService) *WorkspaceHandler {
	return &WorkspaceHandler{svc: svc}
}

func (h *WorkspaceHandler) GetWorkspace(c echo.Context) error {
	userID := c.Get("user_id").(string)
	uid, err := uuid.Parse(userID)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid user id"})
	}

	ws, err := h.svc.GetWorkspace(c.Request().Context(), uid)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "workspace not found"})
	}
	return c.JSON(http.StatusOK, workspaceToMap(ws))
}

type patchWorkspaceRequest struct {
	BusinessName   *string `json:"business_name"`
	Industry       *string `json:"industry"`
	MonthlyBudget  *string `json:"monthly_budget"`
	PrimaryGoal    *string `json:"primary_goal"`
	TargetAudience *string `json:"target_audience"`
}

func (h *WorkspaceHandler) PatchWorkspace(c echo.Context) error {
	userID := c.Get("user_id").(string)
	uid, err := uuid.Parse(userID)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid user id"})
	}

	var req patchWorkspaceRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	input := service.UpdateWorkspaceInput{
		BusinessName:   req.BusinessName,
		Industry:       req.Industry,
		MonthlyBudget:  req.MonthlyBudget,
		PrimaryGoal:    req.PrimaryGoal,
		TargetAudience: req.TargetAudience,
	}

	ws, err := h.svc.UpdateWorkspace(c.Request().Context(), uid, input)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to update workspace"})
	}
	return c.JSON(http.StatusOK, workspaceToMap(ws))
}

func workspaceToMap(ws db.Workspace) map[string]interface{} {
	m := map[string]interface{}{
		"id":                  formatUUID(ws.ID),
		"onboarding_complete": ws.OnboardingComplete,
	}
	if ws.BusinessName.Valid {
		m["business_name"] = ws.BusinessName.String
	}
	if ws.Industry.Valid {
		m["industry"] = ws.Industry.String
	}
	if ws.MonthlyBudget.Valid && ws.MonthlyBudget.Int != nil {
		m["monthly_budget"] = ws.MonthlyBudget.Int.String()
	}
	if ws.PrimaryGoal.Valid {
		m["primary_goal"] = ws.PrimaryGoal.String
	}
	if ws.TargetAudience.Valid {
		m["target_audience"] = ws.TargetAudience.String
	}
	return m
}

func formatUUID(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}
