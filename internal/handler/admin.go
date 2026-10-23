package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/awomore/Pill4rsBE/internal/config"
	"github.com/awomore/Pill4rsBE/internal/crypto"
	"github.com/awomore/Pill4rsBE/internal/service"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// AdminHandler is the Pill4rs platform-operator API.
type AdminHandler struct {
	svc *service.AdminService
	cfg *config.Config
}

func NewAdminHandler(svc *service.AdminService, cfg *config.Config) *AdminHandler {
	return &AdminHandler{svc: svc, cfg: cfg}
}

func (h *AdminHandler) Overview(c echo.Context) error {
	out, err := h.svc.Overview(c.Request().Context())
	if err != nil {
		return adminError(c, err)
	}
	return c.JSON(http.StatusOK, out)
}

func (h *AdminHandler) Agencies(c echo.Context) error {
	rows, err := h.svc.ListAgencies(c.Request().Context())
	if err != nil {
		return adminError(c, err)
	}
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, map[string]any{
			"id":          formatUUID(r.ID),
			"name":        r.Name,
			"owner_email": r.OwnerEmail,
			"clients":     r.Clients,
			"members":     r.Members,
			"created_at":  r.CreatedAt.Time,
		})
	}
	return c.JSON(http.StatusOK, out)
}

func (h *AdminHandler) Users(c echo.Context) error {
	rows, err := h.svc.ListUsers(c.Request().Context())
	if err != nil {
		return adminError(c, err)
	}
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, map[string]any{
			"id":               formatUUID(r.ID),
			"email":            r.Email,
			"agencies":         r.Agencies,
			"owned_workspaces": r.OwnedWorkspaces,
			"created_at":       r.CreatedAt.Time,
		})
	}
	return c.JSON(http.StatusOK, out)
}

func (h *AdminHandler) Admins(c echo.Context) error {
	rows, err := h.svc.ListAdmins(c.Request().Context())
	if err != nil {
		return adminError(c, err)
	}
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, map[string]any{
			"user_id":    formatUUID(r.UserID),
			"email":      r.Email,
			"granted_by": formatUUID(r.GrantedBy),
			"created_at": r.CreatedAt.Time,
		})
	}
	return c.JSON(http.StatusOK, out)
}

type grantAdminRequest struct {
	Email string `json:"email"`
}

func (h *AdminHandler) GrantAdmin(c echo.Context) error {
	adminID, err := uuid.Parse(userIDOf(c))
	if err != nil {
		return c.JSON(http.StatusForbidden, map[string]string{"error": "platform admin session required"})
	}
	var req grantAdminRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "invalid request body")
	}
	row, err := h.svc.GrantByEmail(c.Request().Context(), adminID, req.Email)
	if err != nil {
		return adminError(c, err)
	}
	return c.JSON(http.StatusCreated, map[string]any{"user_id": formatUUID(row.UserID)})
}

func (h *AdminHandler) RevokeAdmin(c echo.Context) error {
	adminID, err := uuid.Parse(userIDOf(c))
	if err != nil {
		return c.JSON(http.StatusForbidden, map[string]string{"error": "platform admin session required"})
	}
	target, err := uuid.Parse(c.Param("user_id"))
	if err != nil {
		return badRequest(c, "invalid user id")
	}
	if err := h.svc.Revoke(c.Request().Context(), adminID, target); err != nil {
		return adminError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

// SwitchWorkspace issues an access cookie for any workspace (support/impersonation).
func (h *AdminHandler) SwitchWorkspace(c echo.Context) error {
	adminID, err := uuid.Parse(userIDOf(c))
	if err != nil {
		return c.JSON(http.StatusForbidden, map[string]string{"error": "platform admin session required"})
	}
	workspaceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return badRequest(c, "invalid workspace id")
	}
	ws, err := h.svc.SwitchWorkspace(c.Request().Context(), adminID, workspaceID)
	if err != nil {
		return adminError(c, err)
	}
	token, err := crypto.SignAccessToken(adminID.String(), formatUUID(ws.ID), []byte(h.cfg.JWTSecret))
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to switch workspace"})
	}
	c.SetCookie(&http.Cookie{
		Name:     "access_token",
		Value:    token,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Path:     "/",
		MaxAge:   900,
	})
	if refresh, rerr := c.Cookie("refresh_token"); rerr == nil && refresh.Value != "" {
		_ = h.svc.SetSessionWorkspace(c.Request().Context(), refresh.Value, workspaceID)
	}
	return c.JSON(http.StatusOK, map[string]any{
		"workspace_id": formatUUID(ws.ID),
		"agency_id":    formatUUID(ws.AgencyID),
		"name":         textOrEmpty(ws.BusinessName),
	})
}

type adminTopUpRequest struct {
	Currency    string `json:"currency"`
	AmountMinor int64  `json:"amount_minor"`
	Reference   string `json:"reference"`
}

func (h *AdminHandler) TopUp(c echo.Context) error {
	adminID, err := uuid.Parse(userIDOf(c))
	if err != nil {
		return c.JSON(http.StatusForbidden, map[string]string{"error": "platform admin session required"})
	}
	workspaceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return badRequest(c, "invalid workspace id")
	}
	var req adminTopUpRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "invalid request body")
	}
	tx, err := h.svc.ManualTopUp(c.Request().Context(), adminID, workspaceID, req.Currency, req.AmountMinor, req.Reference)
	if err != nil {
		return adminError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{
		"id":                  formatUUID(tx.ID),
		"currency":            tx.Currency,
		"amount_minor":        tx.AmountMinor,
		"balance_after_minor": tx.BalanceAfterMinor,
	})
}

func (h *AdminHandler) AuditLogs(c echo.Context) error {
	limit := int32(50)
	offset := int32(0)
	if v := c.QueryParam("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = int32(n)
		}
	}
	if v := c.QueryParam("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			offset = int32(n)
		}
	}
	rows, err := h.svc.ListAuditLogs(c.Request().Context(), limit, offset)
	if err != nil {
		return adminError(c, err)
	}
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		m := map[string]any{
			"id":          formatUUID(r.ID),
			"admin_email": r.AdminEmail,
			"action":      r.Action,
			"created_at":  r.CreatedAt.Time,
		}
		if r.TargetType.Valid {
			m["target_type"] = r.TargetType.String
		}
		if r.TargetID.Valid {
			m["target_id"] = r.TargetID.String
		}
		out = append(out, m)
	}
	return c.JSON(http.StatusOK, out)
}

func adminError(c echo.Context, err error) error {
	var ve service.ValidationError
	switch {
	case errors.As(err, &ve):
		return badRequest(c, ve.Msg)
	default:
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "admin request failed"})
	}
}
