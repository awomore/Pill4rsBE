package service

import (
	"context"
	"log/slog"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/awomore/Pill4rsBE/internal/crypto"
	"github.com/awomore/Pill4rsBE/internal/db"
	"github.com/awomore/Pill4rsBE/internal/integrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type SyncService struct {
	queries *db.Queries
	encKey  string
	sources map[string]integrations.CampaignDataSource
}

// NewSyncService indexes the supplied data sources by platform; sync iterates
// every connected account and uses the matching source, so adding a platform is
// just passing another source.
func NewSyncService(queries *db.Queries, encKey string, sources ...integrations.CampaignDataSource) *SyncService {
	m := make(map[string]integrations.CampaignDataSource, len(sources))
	for _, src := range sources {
		m[src.Platform()] = src
	}
	return &SyncService{queries: queries, encKey: encKey, sources: m}
}

// SyncSummary is the result of a sync run.
type SyncSummary struct {
	Campaigns int `json:"campaigns"`
	Snapshots int `json:"snapshots"`
}

// SyncWorkspace pulls the last 30 days of campaign + insight data for every ad
// account in a workspace and upserts it. It is fully idempotent: re-running it
// updates existing rows in place (campaigns are keyed on
// (ad_account_id, external_campaign_id) and snapshots on (campaign_id, date)).
//
// CRON / WORKER ENTRYPOINT
// ------------------------
// In production a background job runs every 6 hours and calls this exact
// function for each workspace, e.g. in cmd/worker or a scheduler goroutine:
//
//	ticker := time.NewTicker(6 * time.Hour)
//	for range ticker.C {
//	    ids, _ := queries.ListWorkspaceIDs(ctx)
//	    for _, id := range ids {
//	        if _, err := syncService.SyncWorkspace(ctx, id); err != nil {
//	            slog.Error("scheduled sync failed", "workspace", id, "error", err)
//	        }
//	    }
//	}
//
// The POST /api/sync/trigger handler invokes the very same SyncWorkspace
// synchronously for the current user's workspace.
func (s *SyncService) SyncWorkspace(ctx context.Context, workspaceID uuid.UUID) (SyncSummary, error) {
	var summary SyncSummary

	pgWID := pgtype.UUID{Bytes: workspaceID, Valid: true}
	accounts, err := s.queries.GetAdAccountsByWorkspace(ctx, pgWID)
	if err != nil {
		return summary, err
	}

	until := time.Now().UTC()
	since := until.AddDate(0, 0, -30)
	sinceStr := since.Format("2006-01-02")
	untilStr := until.Format("2006-01-02")

	for _, acct := range accounts {
		source, ok := s.sources[acct.Platform]
		if !ok {
			continue
		}

		token, err := crypto.DecryptToken(acct.AccessTokenEncrypted, s.encKey)
		if err != nil {
			slog.Error("sync: decrypt token failed", "ad_account", formatUUID(acct.ID), "error", err)
			continue
		}

		campaigns, err := source.FetchCampaigns(ctx, token, acct.ExternalAccountID)
		if err != nil {
			slog.Error("sync: fetch campaigns failed", "ad_account", formatUUID(acct.ID), "platform", acct.Platform, "error", err)
			continue
		}

		for _, mc := range campaigns {
			dbCamp, err := s.queries.UpsertCampaign(ctx, db.UpsertCampaignParams{
				WorkspaceID:        pgWID,
				AdAccountID:        acct.ID,
				ExternalCampaignID: mc.ExternalID,
				Name:               mc.Name,
				Objective:          textOrNull(mc.Objective),
				Status:             mc.Status,
				DailyBudget:        numericFromFloat(mc.DailyBudget),
			})
			if err != nil {
				slog.Error("sync: upsert campaign failed", "campaign", mc.ExternalID, "error", err)
				continue
			}
			summary.Campaigns++

			insights, err := source.FetchInsights(ctx, token, acct.ExternalAccountID, mc.ExternalID, sinceStr, untilStr)
			if err != nil {
				slog.Error("sync: fetch insights failed", "campaign", mc.ExternalID, "error", err)
				continue
			}

			for _, ni := range insights {
				if ni.Date.IsZero() {
					continue
				}
				_, err := s.queries.UpsertPerformanceSnapshot(ctx, db.UpsertPerformanceSnapshotParams{
					CampaignID:  dbCamp.ID,
					Date:        pgtype.Date{Time: ni.Date, Valid: true},
					Spend:       numericFromFloat(ni.Spend),
					Impressions: ni.Impressions,
					Clicks:      ni.Clicks,
					Conversions: ni.Conversions,
					Reach:       ni.Reach,
					Cpm:         numericFromFloat(ni.CPM),
					Cpc:         numericFromFloat(ni.CPC),
					Ctr:         numericFromFloat(ni.CTR),
					Roas:        numericFromFloat(ni.ROAS),
					Currency:    ni.Currency,
				})
				if err != nil {
					slog.Error("sync: upsert snapshot failed", "campaign", mc.ExternalID, "date", ni.Date, "error", err)
					continue
				}
				summary.Snapshots++
			}
		}
	}

	return summary, nil
}

func textOrNull(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

// budgetToNumeric converts Meta's daily_budget (an integer string in minor
// currency units, e.g. kobo/cents) into a major-unit NUMERIC value.
func budgetToNumeric(s string) pgtype.Numeric {
	s = strings.TrimSpace(s)
	if s == "" {
		return pgtype.Numeric{}
	}
	n := new(big.Int)
	if _, ok := n.SetString(s, 10); !ok {
		return pgtype.Numeric{}
	}
	return pgtype.Numeric{Int: n, Exp: -2, Valid: true}
}

func numericFromFloat(f float64) pgtype.Numeric {
	var n pgtype.Numeric
	if err := n.Scan(strconv.FormatFloat(f, 'f', -1, 64)); err != nil {
		return pgtype.Numeric{}
	}
	return n
}
