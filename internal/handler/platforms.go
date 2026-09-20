package handler

import (
	"log/slog"
	"net/http"
	"sort"

	"github.com/awomore/Pill4rsBE/internal/db"
	"github.com/awomore/Pill4rsBE/internal/platforms"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"
)

type PlatformsHandler struct {
	queries *db.Queries
}

func NewPlatformsHandler(queries *db.Queries) *PlatformsHandler {
	return &PlatformsHandler{queries: queries}
}

// Capabilities returns the static per-platform capability matrix.
func (h *PlatformsHandler) Capabilities(c echo.Context) error {
	caps, err := platforms.Capabilities()
	if err != nil {
		slog.Error("capabilities load failed", "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not load capabilities"})
	}
	return c.JSON(http.StatusOK, caps)
}

// PlatformRow is one ad platform with the workspace's connection state.
type PlatformRow struct {
	Platform     string                         `json:"platform"`
	Connected    bool                           `json:"connected"`
	Accounts     int                            `json:"accounts"`
	Capabilities platforms.PlatformCapabilities `json:"capabilities"`
}

// Overview lists every known ad platform, whether the workspace has connected
// accounts on it, and its capability matrix — the single view Oma and the UI
// use to reason about which platforms are live.
func (h *PlatformsHandler) Overview(c echo.Context) error {
	wid, err := uuid.Parse(workspaceIDOf(c))
	if err != nil {
		return badRequest(c, "invalid workspace id")
	}

	accounts, err := h.queries.GetAdAccountsByWorkspace(c.Request().Context(), pgtype.UUID{Bytes: wid, Valid: true})
	if err != nil {
		slog.Error("platform overview accounts failed", "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to load platforms"})
	}
	counts := map[string]int{}
	for _, a := range accounts {
		counts[a.Platform]++
	}

	caps, _ := platforms.Capabilities()
	capsByKey := map[string]platforms.PlatformCapabilities{}
	if caps != nil {
		capsByKey = caps.Capabilities
	}

	keys := map[string]bool{}
	for k := range capsByKey {
		keys[k] = true
	}
	for k := range counts {
		keys[k] = true
	}

	rows := make([]PlatformRow, 0, len(keys))
	for k := range keys {
		rows = append(rows, PlatformRow{
			Platform:     k,
			Connected:    counts[k] > 0,
			Accounts:     counts[k],
			Capabilities: capsByKey[k],
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Platform < rows[j].Platform })

	return c.JSON(http.StatusOK, map[string]any{"platforms": rows})
}
