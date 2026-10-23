package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/awomore/Pill4rsBE/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// AdminService is the platform-operator layer above agencies.
type AdminService struct {
	queries *db.Queries
	wallet  *WalletService
}

func NewAdminService(queries *db.Queries, wallet *WalletService) *AdminService {
	return &AdminService{queries: queries, wallet: wallet}
}

// IsAdmin reports whether a user is a Pill4rs platform admin.
func (s *AdminService) IsAdmin(ctx context.Context, userID uuid.UUID) (bool, error) {
	return s.queries.IsPlatformAdmin(ctx, pgUUID(userID))
}

// Bootstrap grants platform admin to configured emails that already exist.
func (s *AdminService) Bootstrap(ctx context.Context, emails []string) {
	for _, email := range emails {
		email = strings.TrimSpace(strings.ToLower(email))
		if email == "" {
			continue
		}
		user, err := s.queries.GetUserByEmail(ctx, email)
		if err != nil {
			if err != pgx.ErrNoRows {
				slog.Error("admin bootstrap lookup failed", "email", email, "error", err)
			}
			continue
		}
		if _, err := s.queries.UpsertPlatformAdmin(ctx, db.UpsertPlatformAdminParams{UserID: user.ID}); err != nil {
			slog.Error("admin bootstrap grant failed", "email", email, "error", err)
			continue
		}
		slog.Info("platform admin bootstrapped", "email", email)
	}
}

// GrantByEmail promotes an existing user to platform admin.
func (s *AdminService) GrantByEmail(ctx context.Context, adminID uuid.UUID, email string) (db.PlatformAdmin, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" {
		return db.PlatformAdmin{}, ValidationError{"email is required"}
	}
	user, err := s.queries.GetUserByEmail(ctx, email)
	if err != nil {
		if err == pgx.ErrNoRows {
			return db.PlatformAdmin{}, ValidationError{"no user exists with that email"}
		}
		return db.PlatformAdmin{}, err
	}
	row, err := s.queries.UpsertPlatformAdmin(ctx, db.UpsertPlatformAdminParams{
		UserID:    user.ID,
		GrantedBy: pgUUID(adminID),
	})
	if err != nil {
		return db.PlatformAdmin{}, err
	}
	s.audit(ctx, adminID, "grant_platform_admin", "user", formatUUID(user.ID), map[string]any{"email": email})
	return row, nil
}

// Revoke removes platform admin.
func (s *AdminService) Revoke(ctx context.Context, adminID, targetUserID uuid.UUID) error {
	if err := s.queries.DeletePlatformAdmin(ctx, pgUUID(targetUserID)); err != nil {
		return err
	}
	s.audit(ctx, adminID, "revoke_platform_admin", "user", targetUserID.String(), nil)
	return nil
}

// ListAdmins returns every platform admin.
func (s *AdminService) ListAdmins(ctx context.Context) ([]db.PlatformAdminRow, error) {
	return s.queries.ListPlatformAdmins(ctx)
}

// Overview is the global platform snapshot.
type AdminOverview struct {
	Users           int64               `json:"users"`
	Agencies        int64               `json:"agencies"`
	Workspaces      int64               `json:"workspaces"`
	Campaigns       int64               `json:"campaigns"`
	ActiveCampaigns int64               `json:"active_campaigns"`
	AdAccounts      int64               `json:"ad_accounts"`
	Wallets         []AgencyWalletTotal `json:"wallets"`
}

func (s *AdminService) Overview(ctx context.Context) (AdminOverview, error) {
	row, err := s.queries.GetAdminOverview(ctx)
	if err != nil {
		return AdminOverview{}, err
	}
	wallets, err := s.queries.GetAdminWalletTotals(ctx)
	if err != nil {
		return AdminOverview{}, err
	}
	out := AdminOverview{
		Users:           row.Users,
		Agencies:        row.Agencies,
		Workspaces:      row.Workspaces,
		Campaigns:       row.Campaigns,
		ActiveCampaigns: row.ActiveCampaigns,
		AdAccounts:      row.AdAccounts,
	}
	for _, w := range wallets {
		out.Wallets = append(out.Wallets, AgencyWalletTotal{
			Currency:       w.Currency,
			AvailableMinor: w.AvailableMinor,
			HeldMinor:      w.HeldMinor,
			CashMinor:      w.AvailableMinor + w.HeldMinor,
		})
	}
	return out, nil
}

func (s *AdminService) ListAgencies(ctx context.Context) ([]db.AdminAgencyRow, error) {
	return s.queries.ListAdminAgencies(ctx)
}

func (s *AdminService) ListUsers(ctx context.Context) ([]db.AdminUserRow, error) {
	return s.queries.ListAdminUsers(ctx)
}

// SwitchWorkspace loads any workspace for support/impersonation.
func (s *AdminService) SwitchWorkspace(ctx context.Context, adminID, workspaceID uuid.UUID) (db.Workspace, error) {
	ws, err := s.queries.GetWorkspaceByID(ctx, pgUUID(workspaceID))
	if err != nil {
		if err == pgx.ErrNoRows {
			return db.Workspace{}, ValidationError{"workspace not found"}
		}
		return db.Workspace{}, err
	}
	s.audit(ctx, adminID, "switch_workspace", "workspace", workspaceID.String(), nil)
	return ws, nil
}

// SetSessionWorkspace keeps the admin's refresh session on the selected client.
func (s *AdminService) SetSessionWorkspace(ctx context.Context, refreshToken string, workspaceID uuid.UUID) error {
	return s.queries.SetSessionWorkspaceByHash(ctx, db.SetSessionWorkspaceByHashParams{
		RefreshTokenHash: hashToken(refreshToken),
		WorkspaceID:      pgUUID(workspaceID),
	})
}

// ManualTopUp credits a workspace wallet from the admin console.
func (s *AdminService) ManualTopUp(ctx context.Context, adminID, workspaceID uuid.UUID, currency string, amountMinor int64, reference string) (db.WalletTransaction, error) {
	if strings.TrimSpace(reference) == "" {
		reference = "admin-" + uuid.NewString()
	}
	t, err := s.wallet.TopUp(ctx, workspaceID, currency, amountMinor, "admin:"+adminID.String(), reference)
	if err != nil {
		return db.WalletTransaction{}, err
	}
	s.audit(ctx, adminID, "manual_topup", "workspace", workspaceID.String(), map[string]any{
		"currency": currency, "amount_minor": amountMinor, "reference": reference,
	})
	return t, nil
}

func (s *AdminService) ListAuditLogs(ctx context.Context, limit, offset int32) ([]db.AdminAuditLogRow, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	return s.queries.ListAdminAuditLogs(ctx, db.ListAdminAuditLogsParams{Limit: limit, Offset: offset})
}

func (s *AdminService) audit(ctx context.Context, adminID uuid.UUID, action, targetType, targetID string, metadata map[string]any) {
	var raw []byte
	if len(metadata) > 0 {
		raw, _ = json.Marshal(metadata)
	}
	if _, err := s.queries.InsertAdminAuditLog(ctx, db.InsertAdminAuditLogParams{
		AdminUserID: pgUUID(adminID),
		Action:      action,
		TargetType:  textOrNull(targetType),
		TargetID:    textOrNull(targetID),
		Metadata:    raw,
	}); err != nil {
		slog.Error("admin audit log failed", "action", action, "error", err)
	}
}

// ListAgencyClients returns every client workspace under an agency.
func (s *AdminService) ListAgencyClients(ctx context.Context, agencyID uuid.UUID) ([]db.Workspace, error) {
	return s.queries.ListAgencyWorkspaces(ctx, pgUUID(agencyID))
}
