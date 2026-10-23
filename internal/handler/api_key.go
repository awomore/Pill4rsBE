package handler

import (
	"errors"
	"net/http"

	"github.com/awomore/Pill4rsBE/internal/db"
	"github.com/awomore/Pill4rsBE/internal/service"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"
)

// APIKeyHandler manages workspace API keys used by external brands automating
// campaigns through the stable /api/v1 surface.
type APIKeyHandler struct {
	service *service.APIKeyService
	queries *db.Queries
}

func NewAPIKeyHandler(svc *service.APIKeyService, queries *db.Queries) *APIKeyHandler {
	return &APIKeyHandler{service: svc, queries: queries}
}

type createAPIKeyRequest struct {
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`
}

// Create issues a key and returns the plaintext exactly once.
func (h *APIKeyHandler) Create(c echo.Context) error {
	wid, err := uuid.Parse(workspaceIDOf(c))
	if err != nil {
		return badRequest(c, "invalid workspace id")
	}

	var req createAPIKeyRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "invalid request body")
	}

	var agencyID *uuid.UUID
	if ws, werr := h.queries.GetWorkspaceByID(c.Request().Context(), pgtype.UUID{Bytes: wid, Valid: true}); werr == nil && ws.AgencyID.Valid {
		a := uuid.UUID(ws.AgencyID.Bytes)
		agencyID = &a
	}

	raw, row, err := h.service.Create(c.Request().Context(), wid, agencyID, req.Name, req.Scopes)
	if err != nil {
		var ve service.ValidationError
		if errors.As(err, &ve) {
			return badRequest(c, ve.Msg)
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create api key"})
	}

	return c.JSON(http.StatusCreated, map[string]any{
		"id":         formatUUID(row.ID),
		"name":       row.Name,
		"prefix":     row.Prefix,
		"scopes":     row.Scopes,
		"created_at": row.CreatedAt.Time,
		"key":        raw,
		"warning":    "store this key now; it will not be shown again",
	})
}

// List returns the workspace's keys without hashes or plaintext.
func (h *APIKeyHandler) List(c echo.Context) error {
	wid, err := uuid.Parse(workspaceIDOf(c))
	if err != nil {
		return badRequest(c, "invalid workspace id")
	}

	rows, err := h.service.List(c.Request().Context(), wid)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to list api keys"})
	}

	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		item := map[string]any{
			"id":         formatUUID(row.ID),
			"name":       row.Name,
			"prefix":     row.Prefix,
			"scopes":     row.Scopes,
			"created_at": row.CreatedAt.Time,
		}
		if row.LastUsedAt.Valid {
			item["last_used_at"] = row.LastUsedAt.Time
		}
		if row.RevokedAt.Valid {
			item["revoked_at"] = row.RevokedAt.Time
		}
		out = append(out, item)
	}
	return c.JSON(http.StatusOK, out)
}

// Revoke disables a key immediately.
func (h *APIKeyHandler) Revoke(c echo.Context) error {
	wid, err := uuid.Parse(workspaceIDOf(c))
	if err != nil {
		return badRequest(c, "invalid workspace id")
	}
	kid, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return badRequest(c, "invalid api key id")
	}
	if err := h.service.Revoke(c.Request().Context(), wid, kid); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to revoke api key"})
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}
