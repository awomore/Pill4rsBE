package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/awomore/Pill4rsBE/internal/crypto"
	"github.com/awomore/Pill4rsBE/internal/db"
	"github.com/awomore/Pill4rsBE/internal/integrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	// Status is the source of truth for where a campaign lives:
	//   DRAFT  - delivery tree not fully built (not deliverable yet)
	//   PAUSED - full tree exists on the platform but is not spending
	//   ACTIVE - spending
	StatusDraft  = "DRAFT"
	StatusPaused = "PAUSED"
	StatusActive = "ACTIVE"

	// draftIDPrefix marks an external_campaign_id whose campaign was never pushed.
	draftIDPrefix = "draft_"

	MinDailyBudget = 1.0
	MaxDailyBudget = 10_000_000.0
	maxNameLength  = 200
)

var (
	// ErrNoCampaignsCreated is returned when not a single platform produced a row.
	ErrNoCampaignsCreated = errors.New("no campaigns could be created")
	// ErrCampaignNotFound is returned when a campaign id is not in the workspace.
	ErrCampaignNotFound = errors.New("campaign not found")
)

// ValidationError carries a user-facing 400 message.
type ValidationError struct{ Msg string }

func (e ValidationError) Error() string { return e.Msg }

type CampaignService struct {
	queries  *db.Queries
	encKey   string
	adapters map[string]integrations.CampaignAdapter
}

// NewCampaignService indexes the supplied adapters by platform. Adding a new
// platform is just passing another adapter — the orchestration never changes.
func NewCampaignService(queries *db.Queries, encKey string, adapters ...integrations.CampaignAdapter) *CampaignService {
	m := make(map[string]integrations.CampaignAdapter, len(adapters))
	for _, a := range adapters {
		m[a.Platform()] = a
	}
	return &CampaignService{queries: queries, encKey: encKey, adapters: m}
}

// CreateCampaignInput is the single user intent fanned out across platforms.
type CreateCampaignInput struct {
	Name              string
	Objective         string
	DailyBudget       float64
	Currency          string
	StartDate         time.Time
	EndDate           time.Time
	CTA               string
	AdAccountIDs      []string
	BidStrategy       string
	BidCap            float64
	PacingType        string
	FrequencyCap      int
	FrequencyCapUnit  string
	Targeting         map[string]any
	Creative          *integrations.CreativeSpec
	Variants          []integrations.VariantSpec
	Provenance        map[string]string

	// Legacy backward compat – if present these are auto-wrapped into Variants.
	// Using Variants directly is preferred.
}

// CreatedCampaignResult is one persisted campaign plus the platform it targets.
type CreatedCampaignResult struct {
	Campaign db.Campaign
	Platform string
}

// CreateResult is the outcome of a multi-platform create.
type CreateResult struct {
	Created  []CreatedCampaignResult
	Warnings []string
}

// ValidateCreateInput validates the generic spec (pure, no I/O).
func ValidateCreateInput(in CreateCampaignInput) error {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return ValidationError{"name is required"}
	}
	if len(name) > maxNameLength {
		return ValidationError{"name is too long (max 200 characters)"}
	}
	if !integrations.ValidObjective(in.Objective) {
		return ValidationError{"objective must be one of: awareness, traffic, engagement, leads, sales, app_promotion"}
	}
	if in.DailyBudget < MinDailyBudget || in.DailyBudget > MaxDailyBudget {
		return ValidationError{fmt.Sprintf("daily_budget must be between %.0f and %.0f", MinDailyBudget, MaxDailyBudget)}
	}
	if len(in.AdAccountIDs) == 0 {
		return ValidationError{"at least one ad account is required"}
	}
	for _, id := range in.AdAccountIDs {
		if _, err := uuid.Parse(strings.TrimSpace(id)); err != nil {
			return ValidationError{"ad_account_ids must be valid account ids"}
		}
	}
	if !in.StartDate.IsZero() && !in.EndDate.IsZero() && in.EndDate.Before(in.StartDate) {
		return ValidationError{"end_date must not be before start_date"}
	}
	if in.BidStrategy != "" {
		switch in.BidStrategy {
		case integrations.BidStrategyLowestCostWithoutCap,
			integrations.BidStrategyLowestCost,
			integrations.BidStrategyCostCap,
			integrations.BidStrategyRoasGoal:
		default:
			return ValidationError{"bid_strategy must be one of: LOWEST_COST_WITHOUT_CAP, LOWEST_COST, COST_CAP, ROAS_GOAL"}
		}
	}
	if in.PacingType != "" && in.PacingType != integrations.PacingStandard && in.PacingType != integrations.PacingAccelerated {
		return ValidationError{"pacing_type must be 'standard' or 'accelerated'"}
	}
	if in.FrequencyCapUnit != "" && in.FrequencyCapUnit != integrations.FreqCapHour &&
		in.FrequencyCapUnit != integrations.FreqCapDay && in.FrequencyCapUnit != integrations.FreqCapWeek {
		return ValidationError{"frequency_cap_time_unit must be 'hour', 'day', or 'week'"}
	}
	if in.FrequencyCap > 0 && in.FrequencyCapUnit == "" {
		return ValidationError{"frequency_cap_time_unit is required when frequency_cap is set"}
	}
	hasLegacyCreative := in.Creative != nil
	hasVariants := len(in.Variants) > 0
	if !hasVariants && !hasLegacyCreative {
		return ValidationError{"either variants or creative is required"}
	}
	if hasVariants {
		for i, v := range in.Variants {
			if v.Creative == nil {
				return ValidationError{fmt.Sprintf("variants[%d].creative is required", i)}
			}
			if !looksLikeURL(v.Creative.LinkURL) {
				return ValidationError{fmt.Sprintf("variants[%d].creative.link_url must be a valid http(s) URL", i)}
			}
			if strings.TrimSpace(v.Creative.PrimaryText) == "" && strings.TrimSpace(v.Creative.Headline) == "" {
				return ValidationError{fmt.Sprintf("variants[%d].creative needs primary_text or headline", i)}
			}
			if !looksLikeURL(v.Creative.ImageURL) {
				return ValidationError{fmt.Sprintf("variants[%d].creative.image_url must be a valid http(s) URL", i)}
			}
		}
	} else {
		if !looksLikeURL(in.Creative.LinkURL) {
			return ValidationError{"creative.link_url must be a valid http(s) URL"}
		}
		if strings.TrimSpace(in.Creative.PrimaryText) == "" && strings.TrimSpace(in.Creative.Headline) == "" {
			return ValidationError{"creative needs primary_text or headline"}
		}
		if !looksLikeURL(in.Creative.ImageURL) {
			return ValidationError{"creative.image_url must be a valid http(s) URL"}
		}
	}
	return nil
}

// CreateCampaign validates the spec, then for each platform attempts to build the
// full deliverable tree (campaign -> ad set -> creative -> ad), all PAUSED. A
// reachable platform yields a live (PAUSED) campaign; an unreachable one is saved
// as a DRAFT with whatever partial IDs we got plus a warning. Partial success is
// still success — it only errors if nothing was created.
func (s *CampaignService) CreateCampaign(ctx context.Context, workspaceID uuid.UUID, in CreateCampaignInput) (CreateResult, error) {
	if err := ValidateCreateInput(in); err != nil {
		return CreateResult{}, err
	}

	pgWID := pgtype.UUID{Bytes: workspaceID, Valid: true}
	accounts, err := s.queries.GetAdAccountsByWorkspace(ctx, pgWID)
	if err != nil {
		return CreateResult{}, fmt.Errorf("load ad accounts: %w", err)
	}
	byID := make(map[string]db.AdAccount, len(accounts))
	for _, a := range accounts {
		byID[formatUUID(a.ID)] = a
	}

	spec := integrations.CampaignSpec{
		Name:             strings.TrimSpace(in.Name),
		Objective:        integrations.CampaignObjective(in.Objective),
		DailyBudget:      in.DailyBudget,
		Currency:         in.Currency,
		StartDate:        in.StartDate,
		EndDate:          in.EndDate,
		CTA:              in.CTA,
		BidStrategy:      in.BidStrategy,
		BidCap:           in.BidCap,
		PacingType:       in.PacingType,
		FrequencyCap:     in.FrequencyCap,
		FrequencyCapUnit: in.FrequencyCapUnit,
		Targeting:        in.Targeting, // legacy compat
		Creative:         in.Creative,  // legacy compat
	}
	if len(in.Variants) > 0 {
		spec.Variants = in.Variants
	} else {
		spec.Variants = []integrations.VariantSpec{{Targeting: in.Targeting, Creative: in.Creative}}
	}
	if spec.BidStrategy == "" {
		spec.BidStrategy = integrations.BidStrategyLowestCostWithoutCap
	}
	if spec.PacingType == "" {
		spec.PacingType = integrations.PacingStandard
	}

	targetingJSON := marshalTargeting(in.Targeting)
	creativeJSON := marshalCreative(in.Creative)
	variantsJSON, _ := json.Marshal(spec.Variants)
	provenanceJSON := marshalProvenance(in.Provenance, "user")

	var result CreateResult
	for _, accountID := range dedupe(in.AdAccountIDs) {
		account, ok := byID[accountID]
		if !ok {
			result.Warnings = append(result.Warnings, fmt.Sprintf("ad account %s is not connected to this workspace; skipped", accountID))
			continue
		}
		adapter, ok := s.adapters[account.Platform]
		if !ok {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s is not a supported platform; skipped", account.Platform))
			continue
		}

		state, attemptErr := s.ensureDeliverable(ctx, adapter, account, spec, integrations.DeliverableState{})
		status := StatusPaused
		externalID := state.CampaignID
		if attemptErr != nil {
			status = StatusDraft
			if externalID == "" {
				externalID = newPlaceholderID()
			}
			slog.Warn("campaign delivery failed; saving draft", "platform", account.Platform, "error", attemptErr)
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s: could not fully create remotely (%v); saved as draft", account.Platform, attemptErr))
		}

		row, err := s.queries.CreateCampaign(ctx, db.CreateCampaignParams{
			WorkspaceID:          pgWID,
			AdAccountID:          account.ID,
			ExternalCampaignID:   externalID,
			Name:                 spec.Name,
			Objective:            textOrNull(in.Objective),
			Status:               status,
			DailyBudget:          numericFromFloat(in.DailyBudget),
			StartDate:            dateOrNull(in.StartDate),
			EndDate:              dateOrNull(in.EndDate),
			Cta:                  textOrNull(in.CTA),
			Targeting:            targetingJSON,
			ExternalAdsetID:      textOrNull(state.AdSetID),
			ExternalAdID:         textOrNull(state.AdID),
			ExternalCreativeID:   textOrNull(state.CreativeID),
			Creative:             creativeJSON,
			BidStrategy:          textOrNull(spec.BidStrategy),
			BidCap:               numericOrNull(spec.BidCap),
			PacingType:           textOrNull(spec.PacingType),
			FrequencyCap:         int32Ptr(spec.FrequencyCap),
			FrequencyCapTimeUnit: textOrNull(spec.FrequencyCapUnit),
			Variants:             variantsJSON,
			Provenance:           provenanceJSON,
		})
		if err != nil {
			slog.Error("failed to persist campaign", "platform", account.Platform, "error", err)
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s: failed to save campaign", account.Platform))
			continue
		}
		result.Created = append(result.Created, CreatedCampaignResult{Campaign: row, Platform: account.Platform})
	}

	if len(result.Created) == 0 {
		return result, ErrNoCampaignsCreated
	}
	return result, nil
}

// LaunchCampaign pushes a DRAFT live: it resumes building the delivery tree from
// whatever was already created, then flips the campaign to PAUSED.
func (s *CampaignService) LaunchCampaign(ctx context.Context, workspaceID, campaignID uuid.UUID) (db.Campaign, string, error) {
	pgWID := pgtype.UUID{Bytes: workspaceID, Valid: true}
	pgID := pgtype.UUID{Bytes: campaignID, Valid: true}

	camp, err := s.queries.GetCampaignByIDForWorkspace(ctx, db.GetCampaignByIDForWorkspaceParams{ID: pgID, WorkspaceID: pgWID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.Campaign{}, "", ErrCampaignNotFound
		}
		return db.Campaign{}, "", fmt.Errorf("load campaign: %w", err)
	}

	if camp.Status != StatusDraft {
		return db.Campaign{}, "", ValidationError{"campaign is not a launchable draft"}
	}

	account, err := s.queries.GetAdAccountByID(ctx, camp.AdAccountID)
	if err != nil {
		return db.Campaign{}, "", ValidationError{"the campaign's ad account no longer exists"}
	}
	adapter, ok := s.adapters[account.Platform]
	if !ok {
		return db.Campaign{}, "", ValidationError{fmt.Sprintf("%s is no longer a supported platform", account.Platform)}
	}

	// Re-validate readiness.
	if strings.TrimSpace(camp.Name) == "" {
		return db.Campaign{}, "", ValidationError{"campaign name is required"}
	}
	budget, ok := numericToFloat(camp.DailyBudget)
	if !ok || budget < MinDailyBudget {
		return db.Campaign{}, "", ValidationError{"campaign has no valid budget"}
	}
	if textValue(account.PageID) == "" {
		return db.Campaign{}, "", ValidationError{"connect a Facebook page to this ad account before launching"}
	}
	if camp.Objective.Valid {
		obj := integrations.CampaignObjective(camp.Objective.String)
		if (obj == integrations.ObjectiveSales || obj == integrations.ObjectiveLeads) && textValue(account.PixelID) == "" {
			return db.Campaign{}, "", ValidationError{"connect a Meta pixel to this ad account before launching a sales or leads campaign"}
		}
	}
	spec := specFromCampaign(camp, budget)
	if spec.Creative == nil || !looksLikeURL(spec.Creative.LinkURL) {
		return db.Campaign{}, "", ValidationError{"campaign is missing creative details"}
	}

	have := integrations.DeliverableState{
		CampaignID: nonPlaceholder(camp.ExternalCampaignID),
		AdSetID:    textValue(camp.ExternalAdsetID),
		CreativeID: textValue(camp.ExternalCreativeID),
		AdID:       textValue(camp.ExternalAdID),
	}

	state, err := s.ensureDeliverable(ctx, adapter, account, spec, have)
	if err != nil {
		// Persist whatever progress was made; keep it a DRAFT so launch can resume.
		externalID := state.CampaignID
		if externalID == "" {
			externalID = camp.ExternalCampaignID
		}
		_, _ = s.queries.UpdateCampaignDeliverable(ctx, db.UpdateCampaignDeliverableParams{
			ID:                 pgID,
			ExternalCampaignID: externalID,
			ExternalAdsetID:    textOrNull(state.AdSetID),
			ExternalCreativeID: textOrNull(state.CreativeID),
			ExternalAdID:       textOrNull(state.AdID),
			Status:             StatusDraft,
		})
		return db.Campaign{}, "", fmt.Errorf("launch failed: %w", err)
	}

	updated, err := s.queries.UpdateCampaignDeliverable(ctx, db.UpdateCampaignDeliverableParams{
		ID:                 pgID,
		ExternalCampaignID: state.CampaignID,
		ExternalAdsetID:    textOrNull(state.AdSetID),
		ExternalCreativeID: textOrNull(state.CreativeID),
		ExternalAdID:       textOrNull(state.AdID),
		Status:             StatusPaused,
	})
	if err != nil {
		return db.Campaign{}, "", fmt.Errorf("persist launch: %w", err)
	}
	return updated, account.Platform, nil
}

// SetCampaignStatus pauses or resumes a live campaign (and persists the status).
func (s *CampaignService) SetCampaignStatus(ctx context.Context, workspaceID, campaignID uuid.UUID, status string) (db.Campaign, string, error) {
	if status != StatusActive && status != StatusPaused {
		return db.Campaign{}, "", ValidationError{"status must be ACTIVE or PAUSED"}
	}

	camp, err := s.queries.GetCampaignByIDForWorkspace(ctx, db.GetCampaignByIDForWorkspaceParams{
		ID:          pgtype.UUID{Bytes: campaignID, Valid: true},
		WorkspaceID: pgtype.UUID{Bytes: workspaceID, Valid: true},
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.Campaign{}, "", ErrCampaignNotFound
		}
		return db.Campaign{}, "", fmt.Errorf("load campaign: %w", err)
	}
	if camp.Status == StatusDraft {
		return db.Campaign{}, "", ValidationError{"launch the campaign before changing its status"}
	}

	account, err := s.queries.GetAdAccountByID(ctx, camp.AdAccountID)
	if err != nil {
		return db.Campaign{}, "", ValidationError{"the campaign's ad account no longer exists"}
	}
	adapter, ok := s.adapters[account.Platform]
	if !ok {
		return db.Campaign{}, "", ValidationError{fmt.Sprintf("%s is no longer a supported platform", account.Platform)}
	}

	token, err := crypto.DecryptToken(account.AccessTokenEncrypted, s.encKey)
	if err != nil {
		return db.Campaign{}, "", fmt.Errorf("decrypt token: %w", err)
	}
	if err := adapter.SetStatus(ctx, token, account.ExternalAccountID, camp.ExternalCampaignID, status); err != nil {
		return db.Campaign{}, "", fmt.Errorf("set status on %s: %w", account.Platform, err)
	}

	updated, err := s.queries.UpdateCampaignStatus(ctx, db.UpdateCampaignStatusParams{
		ID:     pgtype.UUID{Bytes: campaignID, Valid: true},
		Status: status,
	})
	if err != nil {
		return db.Campaign{}, "", fmt.Errorf("persist status: %w", err)
	}
	return updated, account.Platform, nil
}

// UpdateCampaignBudget changes a campaign's daily budget (on the platform's ad
// set when it's live, and always in the local record).
func (s *CampaignService) UpdateCampaignBudget(ctx context.Context, workspaceID, campaignID uuid.UUID, dailyBudget float64) (db.Campaign, string, error) {
	if dailyBudget < MinDailyBudget || dailyBudget > MaxDailyBudget {
		return db.Campaign{}, "", ValidationError{fmt.Sprintf("daily_budget must be between %.0f and %.0f", MinDailyBudget, MaxDailyBudget)}
	}

	camp, err := s.queries.GetCampaignByIDForWorkspace(ctx, db.GetCampaignByIDForWorkspaceParams{
		ID:          pgtype.UUID{Bytes: campaignID, Valid: true},
		WorkspaceID: pgtype.UUID{Bytes: workspaceID, Valid: true},
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.Campaign{}, "", ErrCampaignNotFound
		}
		return db.Campaign{}, "", fmt.Errorf("load campaign: %w", err)
	}

	account, err := s.queries.GetAdAccountByID(ctx, camp.AdAccountID)
	if err != nil {
		return db.Campaign{}, "", ValidationError{"the campaign's ad account no longer exists"}
	}
	adapter, ok := s.adapters[account.Platform]
	if !ok {
		return db.Campaign{}, "", ValidationError{fmt.Sprintf("%s is no longer a supported platform", account.Platform)}
	}

	if camp.Status != StatusDraft && camp.ExternalAdsetID.Valid {
		token, err := crypto.DecryptToken(account.AccessTokenEncrypted, s.encKey)
		if err != nil {
			return db.Campaign{}, "", fmt.Errorf("decrypt token: %w", err)
		}
		if err := adapter.UpdateAdSetBudget(ctx, token, account.ExternalAccountID, camp.ExternalAdsetID.String, dailyBudget); err != nil {
			return db.Campaign{}, "", fmt.Errorf("update budget on %s: %w", account.Platform, err)
		}
	}

	updated, err := s.queries.UpdateCampaignDailyBudget(ctx, db.UpdateCampaignDailyBudgetParams{
		ID:          pgtype.UUID{Bytes: campaignID, Valid: true},
		DailyBudget: numericFromFloat(dailyBudget),
	})
	if err != nil {
		return db.Campaign{}, "", fmt.Errorf("persist budget: %w", err)
	}
	return updated, account.Platform, nil
}

// UpdateProvenance merges provenance overrides into a campaign's provenance JSON.
func (s *CampaignService) UpdateProvenance(ctx context.Context, workspaceID, campaignID uuid.UUID, overrides map[string]string) (db.Campaign, error) {
	camp, err := s.queries.GetCampaignByIDForWorkspace(ctx, db.GetCampaignByIDForWorkspaceParams{
		ID:          pgtype.UUID{Bytes: campaignID, Valid: true},
		WorkspaceID: pgtype.UUID{Bytes: workspaceID, Valid: true},
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.Campaign{}, ErrCampaignNotFound
		}
		return db.Campaign{}, fmt.Errorf("load campaign: %w", err)
	}

	existing := make(map[string]string)
	if len(camp.Provenance) > 0 {
		_ = json.Unmarshal(camp.Provenance, &existing)
	}
	if existing == nil {
		existing = make(map[string]string)
	}
	for k, v := range overrides {
		existing[k] = v
	}
	merged, _ := json.Marshal(existing)
	return s.queries.UpdateCampaignProvenance(ctx, db.UpdateCampaignProvenanceParams{
		ID:         pgtype.UUID{Bytes: campaignID, Valid: true},
		Provenance: merged,
	})
}

func (s *CampaignService) ensureDeliverable(ctx context.Context, adapter integrations.CampaignCreator, account db.AdAccount, spec integrations.CampaignSpec, have integrations.DeliverableState) (integrations.DeliverableState, error) {
	token, err := crypto.DecryptToken(account.AccessTokenEncrypted, s.encKey)
	if err != nil {
		return have, fmt.Errorf("decrypt token: %w", err)
	}
	pa := integrations.PlatformAccount{
		AccountID: account.ExternalAccountID,
		PageID:    textValue(account.PageID),
		PixelID:   textValue(account.PixelID),
	}
	return adapter.EnsureDeliverable(ctx, token, pa, spec, have)
}

func specFromCampaign(camp db.Campaign, budget float64) integrations.CampaignSpec {
	spec := integrations.CampaignSpec{Name: camp.Name, DailyBudget: budget}
	if camp.Objective.Valid {
		spec.Objective = integrations.CampaignObjective(camp.Objective.String)
	}
	if camp.StartDate.Valid {
		spec.StartDate = camp.StartDate.Time
	}
	if camp.EndDate.Valid {
		spec.EndDate = camp.EndDate.Time
	}
	if camp.Cta.Valid {
		spec.CTA = camp.Cta.String
	}
	if len(camp.Targeting) > 0 {
		var m map[string]any
		if err := json.Unmarshal(camp.Targeting, &m); err == nil {
			spec.Targeting = m
		}
	}
	spec.Creative = unmarshalCreative(camp.Creative)
	return spec
}

func newPlaceholderID() string {
	return draftIDPrefix + uuid.NewString()
}

func isPlaceholderID(id string) bool {
	return strings.HasPrefix(id, draftIDPrefix)
}

func nonPlaceholder(id string) string {
	if isPlaceholderID(id) {
		return ""
	}
	return id
}

func looksLikeURL(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

func textValue(t pgtype.Text) string {
	if t.Valid {
		return t.String
	}
	return ""
}

func dateOrNull(t time.Time) pgtype.Date {
	if t.IsZero() {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: t, Valid: true}
}

func marshalTargeting(m map[string]any) []byte {
	if len(m) == 0 {
		return nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return b
}

func marshalCreative(cr *integrations.CreativeSpec) []byte {
	if cr == nil {
		return nil
	}
	b, err := json.Marshal(cr)
	if err != nil {
		return nil
	}
	return b
}

func marshalProvenance(m map[string]string, defaultValue string) []byte {
	if len(m) == 0 {
		return nil
	}
	clone := make(map[string]string, len(m))
	for k, v := range m {
		if v == "" {
			clone[k] = defaultValue
		} else {
			clone[k] = v
		}
	}
	b, _ := json.Marshal(clone)
	return b
}

func numericOrNull(v float64) pgtype.Numeric {
	if v == 0 {
		return pgtype.Numeric{}
	}
	return numericFromFloat(v)
}

func int32Ptr(v int) pgtype.Int4 {
	if v == 0 {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: int32(v), Valid: true}
}

// ProvenanceForField returns the provenance entry for a dot-notation field path
// (e.g. "variants[0].creative.headline") or "user" if none is recorded.
func ProvenanceForField(raw []byte, field string) string {
	if len(raw) == 0 {
		return string(integrations.ProvenanceUser)
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return string(integrations.ProvenanceUser)
	}
	if v, ok := m[field]; ok && v != "" {
		return v
	}
	return string(integrations.ProvenanceUser)
}

// ForecastDelivery calls the platform's delivery estimate API and returns
// estimated reach/spend for the given unsaved targeting spec.
func (s *CampaignService) ForecastDelivery(ctx context.Context, workspaceID uuid.UUID, adAccountID string, spec integrations.ForecastSpec) (*integrations.ForecastResult, error) {
	if strings.TrimSpace(spec.Platform) == "" {
		return nil, ValidationError{"platform is required"}
	}
	if strings.TrimSpace(adAccountID) == "" {
		return nil, ValidationError{"ad_account_id is required"}
	}

	pgWID := pgtype.UUID{Bytes: workspaceID, Valid: true}
	account, err := s.queries.GetAdAccountByID(ctx, pgtype.UUID{Bytes: uuid.MustParse(adAccountID), Valid: true})
	if err != nil {
		return nil, fmt.Errorf("load ad account: %w", err)
	}
	if account.WorkspaceID != pgWID {
		return nil, ValidationError{"ad account does not belong to this workspace"}
	}

	adapter, ok := s.adapters[account.Platform]
	if !ok {
		return nil, ValidationError{fmt.Sprintf("%s is not a supported platform", account.Platform)}
	}
	forecaster, ok := interface{}(adapter).(integrations.CampaignForecaster)
	if !ok {
		return nil, ValidationError{fmt.Sprintf("forecasting is not supported for %s", account.Platform)}
	}

	token, err := crypto.DecryptToken(account.AccessTokenEncrypted, s.encKey)
	if err != nil {
		return nil, fmt.Errorf("decrypt token: %w", err)
	}
	pa := integrations.PlatformAccount{
		AccountID: account.ExternalAccountID,
		PageID:    textValue(account.PageID),
		PixelID:   textValue(account.PixelID),
	}
	return forecaster.EstimateDelivery(ctx, token, pa, spec)
}

func unmarshalCreative(b []byte) *integrations.CreativeSpec {
	if len(b) == 0 {
		return nil
	}
	var cr integrations.CreativeSpec
	if err := json.Unmarshal(b, &cr); err != nil {
		return nil
	}
	return &cr
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

func dedupe(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	var out []string
	for _, it := range items {
		it = strings.TrimSpace(it)
		if it == "" {
			continue
		}
		if _, ok := seen[it]; ok {
			continue
		}
		seen[it] = struct{}{}
		out = append(out, it)
	}
	return out
}
