package handler

import (
	"errors"
	"net/http"

	"github.com/awomore/Pill4rsBE/internal/service"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// UserHandler serves the signed-in user's profile, password, sessions, and
// accessible workspaces.
type UserHandler struct {
	svc *service.UserService
}

func NewUserHandler(svc *service.UserService) *UserHandler {
	return &UserHandler{svc: svc}
}

func (h *UserHandler) Profile(c echo.Context) error {
	userID, ok := sessionUserID(c)
	if !ok {
		return nil
	}
	profile, err := h.svc.Profile(c.Request().Context(), userID)
	if err != nil {
		return userError(c, err)
	}
	return c.JSON(http.StatusOK, profile)
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (h *UserHandler) ChangePassword(c echo.Context) error {
	userID, ok := sessionUserID(c)
	if !ok {
		return nil
	}
	var req changePasswordRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "invalid request body")
	}
	if err := h.svc.ChangePassword(c.Request().Context(), userID, req.CurrentPassword, req.NewPassword); err != nil {
		return userError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

func (h *UserHandler) Sessions(c echo.Context) error {
	userID, ok := sessionUserID(c)
	if !ok {
		return nil
	}
	rows, err := h.svc.ListSessions(c.Request().Context(), userID)
	if err != nil {
		return userError(c, err)
	}
	out := make([]map[string]any, 0, len(rows))
	for _, s := range rows {
		m := map[string]any{
			"id":         formatUUID(s.ID),
			"created_at": s.CreatedAt.Time,
		}
		if s.WorkspaceID.Valid {
			m["workspace_id"] = formatUUID(s.WorkspaceID)
		}
		if s.ExpiresAt.Valid {
			m["expires_at"] = s.ExpiresAt.Time
		}
		out = append(out, m)
	}
	return c.JSON(http.StatusOK, out)
}

func (h *UserHandler) RevokeSession(c echo.Context) error {
	userID, ok := sessionUserID(c)
	if !ok {
		return nil
	}
	sessionID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return badRequest(c, "invalid session id")
	}
	if err := h.svc.RevokeSession(c.Request().Context(), userID, sessionID); err != nil {
		return userError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

func (h *UserHandler) Workspaces(c echo.Context) error {
	userID, ok := sessionUserID(c)
	if !ok {
		return nil
	}
	rows, err := h.svc.ListWorkspaces(c.Request().Context(), userID)
	if err != nil {
		return userError(c, err)
	}
	current := workspaceIDOf(c)
	out := make([]map[string]any, 0, len(rows))
	for _, w := range rows {
		name := ""
		if w.ClientLabel.Valid {
			name = w.ClientLabel.String
		} else if w.BusinessName.Valid {
			name = w.BusinessName.String
		}
		m := map[string]any{
			"id":      formatUUID(w.ID),
			"name":    name,
			"current": formatUUID(w.ID) == current,
		}
		if w.AgencyID.Valid {
			m["agency_id"] = formatUUID(w.AgencyID)
		}
		out = append(out, m)
	}
	return c.JSON(http.StatusOK, out)
}

func sessionUserID(c echo.Context) (uuid.UUID, bool) {
	userID, err := uuid.Parse(userIDOf(c))
	if err != nil {
		_ = c.JSON(http.StatusForbidden, map[string]string{"error": "user session required"})
		return uuid.Nil, false
	}
	return userID, true
}

func userError(c echo.Context, err error) error {
	var ve service.ValidationError
	switch {
	case errors.As(err, &ve):
		return badRequest(c, ve.Msg)
	default:
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "user request failed"})
	}
}
