package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/awomore/Pill4rsBE/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var (
	ErrAgencyNotFound     = errors.New("agency not found")
	ErrAgencyAccessDenied = errors.New("agency access denied")
)

// Agency roles from least to most privileged.
const (
	AgencyRoleViewer        = "viewer"
	AgencyRoleManager       = "manager"
	AgencyRoleAdmin         = "admin"
	AgencyRoleOwner         = "owner"
	AgencyRolePlatformAdmin = "platform_admin"
)

// AgencyService manages the agency -> client workspace hierarchy.
type AgencyService struct {
	queries *db.Queries
}

func NewAgencyService(queries *db.Queries) *AgencyService {
	return &AgencyService{queries: queries}
}

type AgencyContext struct {
	AgencyID    string `json:"agency_id"`
	AgencyName  string `json:"name"`
	Role        string `json:"role"`
	OwnerUserID string `json:"owner_user_id"`
	WorkspaceID string `json:"workspace_id"`
}

type AgencyClient struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Label     string `json:"label,omitempty"`
	CreatedAt string `json:"created_at"`
	Current   bool   `json:"current"`
}

type AgencyMemberInfo struct {
	UserID    string `json:"user_id"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	CreatedAt string `json:"created_at"`
}

// GetContext resolves the agency and the user's role for a workspace.
func (s *AgencyService) GetContext(ctx context.Context, workspaceID, userID uuid.UUID) (AgencyContext, error) {
	ws, err := s.queries.GetWorkspaceByID(ctx, pgtype.UUID{Bytes: workspaceID, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AgencyContext{}, ErrAgencyNotFound
		}
		return AgencyContext{}, err
	}
	if !ws.AgencyID.Valid {
		return AgencyContext{}, ErrAgencyNotFound
	}

	agency, err := s.queries.GetAgencyByID(ctx, ws.AgencyID)
	if err != nil {
		return AgencyContext{}, ErrAgencyNotFound
	}
	member, err := s.queries.GetAgencyMember(ctx, db.GetAgencyMemberParams{
		AgencyID: ws.AgencyID,
		UserID:   pgtype.UUID{Bytes: userID, Valid: true},
	})
	if err != nil {
		if isAdmin, adminErr := s.queries.IsPlatformAdmin(ctx, pgUUID(userID)); adminErr == nil && isAdmin {
			return AgencyContext{
				AgencyID: formatUUID(agency.ID), AgencyName: agency.Name, Role: AgencyRolePlatformAdmin,
				OwnerUserID: formatUUID(agency.OwnerUserID), WorkspaceID: formatUUID(ws.ID),
			}, nil
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return AgencyContext{}, ErrAgencyAccessDenied
		}
		return AgencyContext{}, err
	}

	return AgencyContext{
		AgencyID:    formatUUID(agency.ID),
		AgencyName:  agency.Name,
		Role:        member.Role,
		OwnerUserID: formatUUID(agency.OwnerUserID),
		WorkspaceID: formatUUID(ws.ID),
	}, nil
}

// GetClient resolves a client workspace in the agency and verifies membership.
func (s *AgencyService) GetClient(ctx context.Context, userID, workspaceID uuid.UUID) (db.Workspace, error) {
	ws, err := s.queries.GetWorkspaceByID(ctx, pgtype.UUID{Bytes: workspaceID, Valid: true})
	if err != nil {
		return db.Workspace{}, ErrAgencyNotFound
	}
	if !ws.AgencyID.Valid {
		return db.Workspace{}, ErrAgencyNotFound
	}
	if _, err := s.queries.GetAgencyMember(ctx, db.GetAgencyMemberParams{
		AgencyID: ws.AgencyID,
		UserID:   pgtype.UUID{Bytes: userID, Valid: true},
	}); err != nil {
		if isAdmin, adminErr := s.queries.IsPlatformAdmin(ctx, pgUUID(userID)); adminErr == nil && isAdmin {
			return ws, nil
		}
		return db.Workspace{}, ErrAgencyAccessDenied
	}
	return ws, nil
}

// ListClients returns every client workspace in the current agency.
func (s *AgencyService) ListClients(ctx context.Context, workspaceID, userID uuid.UUID) ([]AgencyClient, error) {
	ac, err := s.GetContext(ctx, workspaceID, userID)
	if err != nil {
		return nil, err
	}
	agencyID, _ := uuid.Parse(ac.AgencyID)
	rows, err := s.queries.ListAgencyWorkspaces(ctx, pgtype.UUID{Bytes: agencyID, Valid: true})
	if err != nil {
		return nil, err
	}
	out := make([]AgencyClient, 0, len(rows))
	for _, w := range rows {
		name := w.ClientLabel.String
		if name == "" {
			name = w.BusinessName.String
		}
		out = append(out, AgencyClient{
			ID:        formatUUID(w.ID),
			Name:      name,
			Label:     w.ClientLabel.String,
			CreatedAt: w.CreatedAt.Time.Format("2006-01-02T15:04:05Z07:00"),
			Current:   formatUUID(w.ID) == ac.WorkspaceID,
		})
	}
	return out, nil
}

// CreateClient creates a new client workspace under the current agency.
func (s *AgencyService) CreateClient(ctx context.Context, workspaceID, userID uuid.UUID, name string) (AgencyClient, error) {
	ac, err := s.GetContext(ctx, workspaceID, userID)
	if err != nil {
		return AgencyClient{}, err
	}
	if !CanManageAgency(ac.Role) {
		return AgencyClient{}, ErrAgencyAccessDenied
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return AgencyClient{}, ValidationError{"client name is required"}
	}
	if len(name) > 120 {
		return AgencyClient{}, ValidationError{"client name is too long (max 120 characters)"}
	}

	agencyID, _ := uuid.Parse(ac.AgencyID)
	ws, err := s.queries.CreateAgencyWorkspace(ctx, db.CreateAgencyWorkspaceParams{
		UserID:       pgtype.UUID{Bytes: userID, Valid: true},
		AgencyID:     pgtype.UUID{Bytes: agencyID, Valid: true},
		BusinessName: pgtype.Text{String: name, Valid: true},
		ClientLabel:  pgtype.Text{String: name, Valid: true},
	})
	if err != nil {
		return AgencyClient{}, fmt.Errorf("create client workspace: %w", err)
	}
	return AgencyClient{
		ID:        formatUUID(ws.ID),
		Name:      name,
		Label:     name,
		CreatedAt: ws.CreatedAt.Time.Format("2006-01-02T15:04:05Z07:00"),
	}, nil
}

// ListMembers returns the agency's team.
func (s *AgencyService) ListMembers(ctx context.Context, workspaceID, userID uuid.UUID) ([]AgencyMemberInfo, error) {
	ac, err := s.GetContext(ctx, workspaceID, userID)
	if err != nil {
		return nil, err
	}
	agencyID, _ := uuid.Parse(ac.AgencyID)
	rows, err := s.queries.ListAgencyMembers(ctx, pgtype.UUID{Bytes: agencyID, Valid: true})
	if err != nil {
		return nil, err
	}
	out := make([]AgencyMemberInfo, 0, len(rows))
	for _, m := range rows {
		out = append(out, AgencyMemberInfo{
			UserID:    formatUUID(m.UserID),
			Email:     m.Email,
			Role:      m.Role,
			CreatedAt: m.CreatedAt.Time.Format("2006-01-02T15:04:05Z07:00"),
		})
	}
	return out, nil
}

// AddMemberByEmail adds an existing user to the agency. Invites are handled by
// the caller's onboarding flow.
func (s *AgencyService) AddMemberByEmail(ctx context.Context, workspaceID, userID uuid.UUID, email, role string) (AgencyMemberInfo, error) {
	ac, err := s.GetContext(ctx, workspaceID, userID)
	if err != nil {
		return AgencyMemberInfo{}, err
	}
	if !CanManageAgency(ac.Role) {
		return AgencyMemberInfo{}, ErrAgencyAccessDenied
	}
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" {
		return AgencyMemberInfo{}, ValidationError{"email is required"}
	}
	role, err = NormalizeAgencyRole(role)
	if err != nil {
		return AgencyMemberInfo{}, err
	}
	if role == AgencyRoleOwner {
		return AgencyMemberInfo{}, ValidationError{"ownership transfer is not supported"}
	}

	user, err := s.queries.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AgencyMemberInfo{}, ValidationError{"no user exists with that email; ask them to sign up first"}
		}
		return AgencyMemberInfo{}, err
	}

	agencyID, _ := uuid.Parse(ac.AgencyID)
	member, err := s.queries.AddAgencyMember(ctx, db.AddAgencyMemberParams{
		AgencyID: pgtype.UUID{Bytes: agencyID, Valid: true},
		UserID:   user.ID,
		Role:     role,
	})
	if err != nil {
		return AgencyMemberInfo{}, fmt.Errorf("add agency member: %w", err)
	}
	return AgencyMemberInfo{
		UserID:    formatUUID(member.UserID),
		Email:     email,
		Role:      member.Role,
		CreatedAt: member.CreatedAt.Time.Format("2006-01-02T15:04:05Z07:00"),
	}, nil
}

// UpdateMemberRole changes a member's role. The owner cannot be changed.
func (s *AgencyService) UpdateMemberRole(ctx context.Context, workspaceID, userID, targetUserID uuid.UUID, role string) error {
	ac, err := s.GetContext(ctx, workspaceID, userID)
	if err != nil {
		return err
	}
	if !CanManageAgency(ac.Role) {
		return ErrAgencyAccessDenied
	}
	if formatUUID(pgtype.UUID{Bytes: targetUserID, Valid: true}) == ac.OwnerUserID {
		return ValidationError{"the agency owner's role cannot be changed"}
	}
	role, err = NormalizeAgencyRole(role)
	if err != nil {
		return err
	}
	if role == AgencyRoleOwner {
		return ValidationError{"ownership transfer is not supported"}
	}
	agencyID, _ := uuid.Parse(ac.AgencyID)
	_, err = s.queries.AddAgencyMember(ctx, db.AddAgencyMemberParams{
		AgencyID: pgtype.UUID{Bytes: agencyID, Valid: true},
		UserID:   pgtype.UUID{Bytes: targetUserID, Valid: true},
		Role:     role,
	})
	return err
}

// RemoveMember removes a non-owner member.
func (s *AgencyService) RemoveMember(ctx context.Context, workspaceID, userID, targetUserID uuid.UUID) error {
	ac, err := s.GetContext(ctx, workspaceID, userID)
	if err != nil {
		return err
	}
	if !CanManageAgency(ac.Role) {
		return ErrAgencyAccessDenied
	}
	if formatUUID(pgtype.UUID{Bytes: targetUserID, Valid: true}) == ac.OwnerUserID {
		return ValidationError{"the agency owner cannot be removed"}
	}
	agencyID, _ := uuid.Parse(ac.AgencyID)
	return s.queries.RemoveAgencyMember(ctx, db.RemoveAgencyMemberParams{
		AgencyID: pgtype.UUID{Bytes: agencyID, Valid: true},
		UserID:   pgtype.UUID{Bytes: targetUserID, Valid: true},
	})
}

// CanManageAgency reports whether a role can manage clients/members.
func CanManageAgency(role string) bool {
	return role == AgencyRoleOwner || role == AgencyRoleAdmin || role == AgencyRolePlatformAdmin
}

// NormalizeAgencyRole validates a role name.
func NormalizeAgencyRole(role string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "":
		return AgencyRoleViewer, nil
	case AgencyRoleViewer:
		return AgencyRoleViewer, nil
	case AgencyRoleManager:
		return AgencyRoleManager, nil
	case AgencyRoleAdmin:
		return AgencyRoleAdmin, nil
	case AgencyRoleOwner:
		return AgencyRoleOwner, nil
	default:
		return "", ValidationError{"role must be owner, admin, manager, or viewer"}
	}
}

// SetSessionWorkspace keeps the refresh session pinned to the switched client.
func (s *AgencyService) SetSessionWorkspace(ctx context.Context, refreshToken string, workspaceID uuid.UUID) error {
	return s.queries.SetSessionWorkspaceByHash(ctx, db.SetSessionWorkspaceByHashParams{
		RefreshTokenHash: hashToken(refreshToken),
		WorkspaceID:      pgtype.UUID{Bytes: workspaceID, Valid: true},
	})
}

type AgencyTotals struct {
	Spend       float64 `json:"spend"`
	Impressions int64   `json:"impressions"`
	Clicks      int64   `json:"clicks"`
	Conversions int64   `json:"conversions"`
	AverageRoas float64 `json:"average_roas"`
	AverageCpc  float64 `json:"average_cpc"`
}

type AgencyWalletTotal struct {
	Currency       string `json:"currency"`
	AvailableMinor int64  `json:"available_minor"`
	HeldMinor      int64  `json:"held_minor"`
	CashMinor      int64  `json:"cash_minor"`
}

type AgencyCommissionTotal struct {
	Currency        string `json:"currency"`
	BaseMinor       int64  `json:"base_minor"`
	CommissionMinor int64  `json:"commission_minor"`
}

type AgencyClientSummary struct {
	WorkspaceID     string  `json:"workspace_id"`
	Name            string  `json:"name"`
	Spend           float64 `json:"spend"`
	Impressions     int64   `json:"impressions"`
	Clicks          int64   `json:"clicks"`
	Conversions     int64   `json:"conversions"`
	Campaigns       int64   `json:"campaigns"`
	ActiveCampaigns int64   `json:"active_campaigns"`
}

type AgencySummary struct {
	AgencyID        string                  `json:"agency_id"`
	Name            string                  `json:"name"`
	Role            string                  `json:"role"`
	Range           string                  `json:"range"`
	StartDate       string                  `json:"start_date"`
	EndDate         string                  `json:"end_date"`
	Clients         int                     `json:"clients"`
	Totals          AgencyTotals            `json:"totals"`
	Campaigns       int64                   `json:"campaigns"`
	ActiveCampaigns int64                   `json:"active_campaigns"`
	Wallets         []AgencyWalletTotal     `json:"wallets"`
	Commissions     []AgencyCommissionTotal `json:"commissions"`
	ClientBreakdown []AgencyClientSummary   `json:"clients_breakdown"`
}

// Summary aggregates performance, wallet, commission, and client-level data
// across every client workspace in the agency.
func (s *AgencyService) Summary(ctx context.Context, workspaceID, userID uuid.UUID, rangeParam string) (AgencySummary, error) {
	ac, err := s.GetContext(ctx, workspaceID, userID)
	if err != nil {
		return AgencySummary{}, err
	}
	days, normalizedRange, ok := agencyRangeDays(rangeParam)
	if !ok {
		return AgencySummary{}, ValidationError{"range must be 7d, 30d, or 90d"}
	}
	end := time.Now().UTC()
	start := end.AddDate(0, 0, -(days - 1))
	agencyID, _ := uuid.Parse(ac.AgencyID)
	pgAgency := pgtype.UUID{Bytes: agencyID, Valid: true}
	startDate := pgtype.Date{Time: start, Valid: true}
	endDate := pgtype.Date{Time: end, Valid: true}

	totals, err := s.queries.GetAgencyTotals(ctx, db.GetAgencyTotalsParams{
		AgencyID: pgAgency, Date: startDate, Date_2: endDate,
	})
	if err != nil {
		return AgencySummary{}, err
	}
	counts, err := s.queries.GetAgencyCampaignCounts(ctx, pgAgency)
	if err != nil {
		return AgencySummary{}, err
	}
	walletRows, err := s.queries.GetAgencyWalletTotals(ctx, pgAgency)
	if err != nil {
		return AgencySummary{}, err
	}
	commissionRows, err := s.queries.GetAgencyCommissionTotals(ctx, db.GetAgencyCommissionTotalsParams{
		AgencyID: pgAgency, Date: startDate, Date_2: endDate,
	})
	if err != nil {
		return AgencySummary{}, err
	}
	clientRows, err := s.queries.GetAgencyClientBreakdown(ctx, db.GetAgencyClientBreakdownParams{
		AgencyID: pgAgency, Date: startDate, Date_2: endDate,
	})
	if err != nil {
		return AgencySummary{}, err
	}

	summary := AgencySummary{
		AgencyID:        ac.AgencyID,
		Name:            ac.AgencyName,
		Role:            ac.Role,
		Range:           normalizedRange,
		StartDate:       start.Format("2006-01-02"),
		EndDate:         end.Format("2006-01-02"),
		Clients:         len(clientRows),
		Campaigns:       counts.TotalCampaigns,
		ActiveCampaigns: counts.ActiveCampaigns,
		Totals: AgencyTotals{
			Impressions: totals.TotalImpressions,
			Clicks:      totals.TotalClicks,
			Conversions: totals.TotalConversions,
			AverageRoas: numericToFloatOrZero(totals.AverageRoas),
			AverageCpc:  numericToFloatOrZero(totals.AverageCpc),
		},
	}
	summary.Totals.Spend = numericToFloatOrZero(totals.TotalSpend)

	for _, w := range walletRows {
		summary.Wallets = append(summary.Wallets, AgencyWalletTotal{
			Currency:       w.Currency,
			AvailableMinor: w.AvailableMinor,
			HeldMinor:      w.HeldMinor,
			CashMinor:      w.AvailableMinor + w.HeldMinor,
		})
	}
	for _, c := range commissionRows {
		summary.Commissions = append(summary.Commissions, AgencyCommissionTotal{
			Currency:        c.Currency,
			BaseMinor:       c.BaseMinor,
			CommissionMinor: c.CommissionMinor,
		})
	}
	for _, c := range clientRows {
		summary.ClientBreakdown = append(summary.ClientBreakdown, AgencyClientSummary{
			WorkspaceID:     formatUUID(c.WorkspaceID),
			Name:            c.Name,
			Spend:           numericToFloatOrZero(c.Spend),
			Impressions:     c.Impressions,
			Clicks:          c.Clicks,
			Conversions:     c.Conversions,
			Campaigns:       c.Campaigns,
			ActiveCampaigns: c.ActiveCampaigns,
		})
	}
	return summary, nil
}

func agencyRangeDays(r string) (int, string, bool) {
	switch r {
	case "", "7d":
		return 7, "7d", true
	case "30d":
		return 30, "30d", true
	case "90d":
		return 90, "90d", true
	default:
		return 0, "", false
	}
}

func numericToFloatOrZero(n pgtype.Numeric) float64 {
	f, _ := numericToFloat(n)
	return f
}
