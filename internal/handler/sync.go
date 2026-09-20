package handler

import (
	"log/slog"
	"net/http"

	"github.com/awomore/Pill4rsBE/internal/db"
	"github.com/awomore/Pill4rsBE/internal/service"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"
)

type SyncHandler struct {
	queries *db.Queries
	sync    *service.SyncService
}

func NewSyncHandler(queries *db.Queries, syncSvc *service.SyncService) *SyncHandler {
	return &SyncHandler{queries: queries, sync: syncSvc}
}

// Trigger runs a synchronous sync for the current user's workspace. It stands in
// for the 6-hour cron: a scheduled worker would call the exact same
// service.SyncService.SyncWorkspace function (see its doc comment).
func (h *SyncHandler) Trigger(c echo.Context) error {
	workspaceID, _ := c.Get("workspace_id").(string)
	wid, err := uuid.Parse(workspaceID)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid workspace id"})
	}

	summary, err := h.sync.SyncWorkspace(c.Request().Context(), wid)
	if err != nil {
		slog.Error("sync trigger failed", "workspace", workspaceID, "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "sync failed"})
	}

	return c.JSON(http.StatusOK, summary)
}

// ListCampaigns returns the workspace's campaigns each with their most recent
// performance snapshot joined in.
func (h *SyncHandler) ListCampaigns(c echo.Context) error {
	workspaceID, _ := c.Get("workspace_id").(string)
	wid, err := uuid.Parse(workspaceID)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid workspace id"})
	}
	pgWID := pgtype.UUID{Bytes: wid, Valid: true}

	rows, err := h.queries.GetCampaignsWithLatestSnapshot(c.Request().Context(), pgWID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to list campaigns"})
	}

	out := make([]map[string]interface{}, 0, len(rows))
	for _, r := range rows {
		campaign := map[string]interface{}{
			"id":                   formatUUID(r.ID),
			"external_campaign_id": r.ExternalCampaignID,
			"name":                 r.Name,
			"status":               r.Status,
			"latest_snapshot":      latestSnapshotMap(r),
		}
		if r.Objective.Valid {
			campaign["objective"] = r.Objective.String
		}
		if f, ok := numericToFloat(r.DailyBudget); ok {
			campaign["daily_budget"] = f
		}
		if r.CreatedAt.Valid {
			campaign["created_at"] = r.CreatedAt.Time
		}
		if r.UpdatedAt.Valid {
			campaign["updated_at"] = r.UpdatedAt.Time
		}
		out = append(out, campaign)
	}

	return c.JSON(http.StatusOK, out)
}

func latestSnapshotMap(r db.GetCampaignsWithLatestSnapshotRow) interface{} {
	if !r.SnapshotID.Valid {
		return nil
	}
	snap := map[string]interface{}{
		"impressions": r.Impressions,
		"clicks":      r.Clicks,
		"conversions": r.Conversions,
		"reach":       r.Reach,
	}
	if r.SnapshotDate.Valid {
		snap["date"] = r.SnapshotDate.Time.Format("2006-01-02")
	}
	if r.Currency != "" {
		snap["currency"] = r.Currency
	}
	for key, val := range map[string]pgtype.Numeric{
		"spend": r.Spend,
		"cpm":   r.Cpm,
		"cpc":   r.Cpc,
		"ctr":   r.Ctr,
		"roas":  r.Roas,
	} {
		if f, ok := numericToFloat(val); ok {
			snap[key] = f
		}
	}
	return snap
}

func numericToFloat(n pgtype.Numeric) (float64, bool) {
	if !n.Valid {
		return 0, false
	}
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		return 0, false
	}
	return f.Float64, true
}
