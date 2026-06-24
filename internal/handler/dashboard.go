package handler

import (
	"net/http"
	"time"

	"github.com/awomore/Pill4rsBE/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"
)

type DashboardHandler struct {
	queries *db.Queries
}

func NewDashboardHandler(queries *db.Queries) *DashboardHandler {
	return &DashboardHandler{queries: queries}
}

// rangeToDays maps the supported range query values to a day count. An empty
// range defaults to 7d.
func rangeToDays(r string) (int, bool) {
	switch r {
	case "", "7d":
		return 7, true
	case "30d":
		return 30, true
	case "90d":
		return 90, true
	default:
		return 0, false
	}
}

// Summary aggregates the workspace's performance snapshots over the requested
// range and returns totals, a day-by-day spend/conversions series, and a
// per-campaign breakdown.
func (h *DashboardHandler) Summary(c echo.Context) error {
	workspaceID, _ := c.Get("workspace_id").(string)
	wid, err := uuid.Parse(workspaceID)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid workspace id"})
	}
	pgWID := pgtype.UUID{Bytes: wid, Valid: true}

	rangeParam := c.QueryParam("range")
	days, ok := rangeToDays(rangeParam)
	if !ok {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid range; expected 7d, 30d, or 90d"})
	}
	if rangeParam == "" {
		rangeParam = "7d"
	}

	end := time.Now().UTC()
	start := end.AddDate(0, 0, -(days - 1))
	startDate := pgtype.Date{Time: start, Valid: true}
	endDate := pgtype.Date{Time: end, Valid: true}
	ctx := c.Request().Context()

	totals, err := h.queries.GetDashboardTotals(ctx, db.GetDashboardTotalsParams{
		WorkspaceID: pgWID, Date: startDate, Date_2: endDate,
	})
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to load summary"})
	}

	seriesRows, err := h.queries.GetDashboardDailySeries(ctx, db.GetDashboardDailySeriesParams{
		WorkspaceID: pgWID, Date: startDate, Date_2: endDate,
	})
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to load summary"})
	}

	breakdownRows, err := h.queries.GetDashboardCampaignBreakdown(ctx, db.GetDashboardCampaignBreakdownParams{
		WorkspaceID: pgWID, Date: startDate, Date_2: endDate,
	})
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to load summary"})
	}

	series := make([]map[string]interface{}, 0, len(seriesRows))
	for _, r := range seriesRows {
		point := map[string]interface{}{
			"spend":       numericFloatOrZero(r.Spend),
			"conversions": r.Conversions,
		}
		if r.Date.Valid {
			point["date"] = r.Date.Time.Format("2006-01-02")
		}
		series = append(series, point)
	}

	campaigns := make([]map[string]interface{}, 0, len(breakdownRows))
	for _, r := range breakdownRows {
		campaigns = append(campaigns, map[string]interface{}{
			"id":          formatUUID(r.CampaignID),
			"name":        r.Name,
			"status":      r.Status,
			"spend":       numericFloatOrZero(r.Spend),
			"impressions": r.Impressions,
			"clicks":      r.Clicks,
			"conversions": r.Conversions,
			"averageRoas": numericFloatOrZero(r.AverageRoas),
			"averageCpc":  numericFloatOrZero(r.AverageCpc),
		})
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"range":            rangeParam,
		"startDate":        start.Format("2006-01-02"),
		"endDate":          end.Format("2006-01-02"),
		"totalSpend":       numericFloatOrZero(totals.TotalSpend),
		"totalImpressions": totals.TotalImpressions,
		"totalClicks":      totals.TotalClicks,
		"totalConversions": totals.TotalConversions,
		"averageRoas":      numericFloatOrZero(totals.AverageRoas),
		"averageCpc":       numericFloatOrZero(totals.AverageCpc),
		"series":           series,
		"campaigns":        campaigns,
	})
}

func numericFloatOrZero(n pgtype.Numeric) float64 {
	if f, ok := numericToFloat(n); ok {
		return f
	}
	return 0
}
