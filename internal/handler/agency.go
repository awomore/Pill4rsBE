package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/awomore/Pill4rsBE/internal/config"
	"github.com/awomore/Pill4rsBE/internal/crypto"
	"github.com/awomore/Pill4rsBE/internal/service"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// AgencyHandler exposes the agency -> client workspace hierarchy and team
// management. It is intended for UI sessions; API keys cannot manage agencies.
type AgencyHandler struct {
	svc *service.AgencyService
	cfg *config.Config
}

func NewAgencyHandler(svc *service.AgencyService, cfg *config.Config) *AgencyHandler {
	return &AgencyHandler{svc: svc, cfg: cfg}
}

func (h *AgencyHandler) Get(c echo.Context) error {
	ws, user, ok := agencyIDs(c)
	if !ok {
		return nil
	}
	ac, err := h.svc.GetContext(c.Request().Context(), ws, user)
	if err != nil {
		return agencyError(c, err)
	}
	return c.JSON(http.StatusOK, ac)
}

func (h *AgencyHandler) Clients(c echo.Context) error {
	ws, user, ok := agencyIDs(c)
	if !ok {
		return nil
	}
	clients, err := h.svc.ListClients(c.Request().Context(), ws, user)
	if err != nil {
		return agencyError(c, err)
	}
	return c.JSON(http.StatusOK, clients)
}

type createClientRequest struct {
	Name string `json:"name"`
}

func (h *AgencyHandler) CreateClient(c echo.Context) error {
	ws, user, ok := agencyIDs(c)
	if !ok {
		return nil
	}
	var req createClientRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "invalid request body")
	}
	client, err := h.svc.CreateClient(c.Request().Context(), ws, user, req.Name)
	if err != nil {
		return agencyError(c, err)
	}
	return c.JSON(http.StatusCreated, client)
}

// SwitchClient issues a new access-token cookie scoped to the target client.
func (h *AgencyHandler) SwitchClient(c echo.Context) error {
	_, user, ok := agencyIDs(c)
	if !ok {
		return nil
	}
	target, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return badRequest(c, "invalid client id")
	}

	client, err := h.svc.GetClient(c.Request().Context(), user, target)
	if err != nil {
		return agencyError(c, err)
	}
	token, err := crypto.SignAccessToken(user.String(), client.ID.String(), []byte(h.cfg.JWTSecret))
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to switch client"})
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
		_ = h.svc.SetSessionWorkspace(c.Request().Context(), refresh.Value, uuid.UUID(client.ID.Bytes))
	}

	name := ""
	if client.ClientLabel.Valid {
		name = client.ClientLabel.String
	} else if client.BusinessName.Valid {
		name = client.BusinessName.String
	}
	return c.JSON(http.StatusOK, map[string]any{
		"workspace_id": formatUUID(client.ID),
		"name":         name,
	})
}

func (h *AgencyHandler) Members(c echo.Context) error {
	ws, user, ok := agencyIDs(c)
	if !ok {
		return nil
	}
	members, err := h.svc.ListMembers(c.Request().Context(), ws, user)
	if err != nil {
		return agencyError(c, err)
	}
	return c.JSON(http.StatusOK, members)
}

type addMemberRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

func (h *AgencyHandler) AddMember(c echo.Context) error {
	ws, user, ok := agencyIDs(c)
	if !ok {
		return nil
	}
	var req addMemberRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "invalid request body")
	}
	member, err := h.svc.AddMemberByEmail(c.Request().Context(), ws, user, req.Email, req.Role)
	if err != nil {
		return agencyError(c, err)
	}
	return c.JSON(http.StatusCreated, member)
}

type updateMemberRequest struct {
	Role string `json:"role"`
}

func (h *AgencyHandler) UpdateMember(c echo.Context) error {
	ws, user, ok := agencyIDs(c)
	if !ok {
		return nil
	}
	target, err := uuid.Parse(c.Param("user_id"))
	if err != nil {
		return badRequest(c, "invalid user id")
	}
	var req updateMemberRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "invalid request body")
	}
	if err := h.svc.UpdateMemberRole(c.Request().Context(), ws, user, target, req.Role); err != nil {
		return agencyError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

func (h *AgencyHandler) RemoveMember(c echo.Context) error {
	ws, user, ok := agencyIDs(c)
	if !ok {
		return nil
	}
	target, err := uuid.Parse(c.Param("user_id"))
	if err != nil {
		return badRequest(c, "invalid user id")
	}
	if err := h.svc.RemoveMember(c.Request().Context(), ws, user, target); err != nil {
		return agencyError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

func agencyIDs(c echo.Context) (uuid.UUID, uuid.UUID, bool) {
	ws, err := uuid.Parse(workspaceIDOf(c))
	if err != nil {
		_ = badRequest(c, "invalid workspace id")
		return uuid.Nil, uuid.Nil, false
	}
	user, err := uuid.Parse(userIDOf(c))
	if err != nil {
		_ = c.JSON(http.StatusForbidden, map[string]string{"error": "agency management requires a user session"})
		return uuid.Nil, uuid.Nil, false
	}
	return ws, user, true
}

func userIDOf(c echo.Context) string {
	id, _ := c.Get("user_id").(string)
	return strings.TrimSpace(id)
}

func agencyError(c echo.Context, err error) error {
	var ve service.ValidationError
	switch {
	case errors.As(err, &ve):
		return badRequest(c, ve.Msg)
	case errors.Is(err, service.ErrAgencyNotFound):
		return c.JSON(http.StatusNotFound, map[string]string{"error": "agency not found"})
	case errors.Is(err, service.ErrAgencyAccessDenied):
		return c.JSON(http.StatusForbidden, map[string]string{"error": "you do not have access to this agency"})
	default:
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "agency request failed"})
	}
}

// Summary returns consolidated performance, wallet, commission, and client
// rollups across the agency. ?range=7d|30d|90d (default 7d).
func (h *AgencyHandler) Summary(c echo.Context) error {
	ws, user, ok := agencyIDs(c)
	if !ok {
		return nil
	}
	summary, err := h.svc.Summary(c.Request().Context(), ws, user, c.QueryParam("range"))
	if err != nil {
		return agencyError(c, err)
	}
	return c.JSON(http.StatusOK, summary)
}
