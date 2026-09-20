package integrations

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Token is the result of exchanging an OAuth authorization code.
type Token struct {
	AccessToken string
	ExpiresAt   time.Time
}

// Account is a single ad account accessible to a connected OAuth identity. A
// business may have several per platform; they're all stored on connect.
type Account struct {
	ID   string
	Name string
}

// NormalizedInsight is the platform-agnostic shape that every ad platform's raw
// daily insight is mapped into before being persisted as a performance snapshot.
// One NormalizedInsight represents one campaign on one day.
type NormalizedInsight struct {
	Date        time.Time
	Spend       float64
	Impressions int64
	Clicks      int64
	Conversions int64
	Reach       int64
	CPM         float64
	CPC         float64
	CTR         float64
	ROAS        float64
	Currency    string
}

// AdPlatform abstracts an advertising platform's OAuth flow so that additional
// platforms (Google Ads, TikTok, etc.) can be added behind the same interface.
type AdPlatform interface {
	// GetOAuthURL builds the platform's OAuth authorization URL, embedding the
	// provided CSRF state parameter.
	GetOAuthURL(state string) string

	// ExchangeCodeForToken exchanges an authorization code for an access token
	// and its expiry.
	ExchangeCodeForToken(ctx context.Context, code string) (*Token, error)
}

// CampaignObjective is the platform-agnostic campaign goal. Each adapter maps it
// to its own native objective.
type CampaignObjective string

const (
	ObjectiveAwareness    CampaignObjective = "awareness"
	ObjectiveTraffic      CampaignObjective = "traffic"
	ObjectiveEngagement   CampaignObjective = "engagement"
	ObjectiveLeads        CampaignObjective = "leads"
	ObjectiveSales        CampaignObjective = "sales"
	ObjectiveAppPromotion CampaignObjective = "app_promotion"
)

// ValidObjective reports whether o is a known internal objective.
func ValidObjective(o string) bool {
	switch CampaignObjective(o) {
	case ObjectiveAwareness, ObjectiveTraffic, ObjectiveEngagement,
		ObjectiveLeads, ObjectiveSales, ObjectiveAppPromotion:
		return true
	default:
		return false
	}
}

// CreativeSpec describes the ad creative (copy + destination + media). On most
// platforms this lives on an ad-creative object below the campaign. Format is the
// media type the creative carries: image, video, gif, audio, or playable.
type CreativeSpec struct {
	PrimaryText  string `json:"primary_text"`
	Headline     string `json:"headline"`
	Description  string `json:"description"`
	LinkURL      string `json:"link_url"`
	ImageURL     string `json:"image_url"`
	VideoURL     string `json:"video_url,omitempty"`
	Format       string `json:"format,omitempty"`
	MediaAssetID string `json:"media_asset_id,omitempty"`
}

// ValidCreativeFormat reports whether f is a known creative format.
func ValidCreativeFormat(f string) bool {
	switch f {
	case "image", "video", "gif", "audio", "playable":
		return true
	default:
		return false
	}
}

// normalizeGenderAliases maps the many input spellings of a gender onto the
// canonical lowercase name ("female", "male", "unknown").
var normalizeGenderAliases = map[string]string{
	"f": "female", "female": "female", "women": "female", "woman": "female", "2": "female",
	"m": "male", "male": "male", "men": "male", "man": "male", "1": "male",
	"unknown": "unknown", "all": "unknown", "0": "unknown",
}

// GenderValues normalizes an arbitrary genders value (from Meta 1/2 ints, or
// strings like "female"/"male") onto a canonical set of lowercase names. It
// returns nil when genders are effectively unconstrained (unset or "all").
func GenderValues(raw any) []string {
	switch v := raw.(type) {
	case []string:
		return normalizeGenderStrings(v)
	case []any:
		ss := make([]string, 0, len(v))
		for _, e := range v {
			ss = append(ss, fmt.Sprint(e))
		}
		return normalizeGenderStrings(ss)
	case []int:
		ss := make([]string, 0, len(v))
		for _, e := range v {
			ss = append(ss, strconv.Itoa(e))
		}
		return normalizeGenderStrings(ss)
	case string:
		return normalizeGenderStrings([]string{v})
	}
	return nil
}

func normalizeGenderStrings(in []string) []string {
	set := make(map[string]struct{}, len(in))
	for _, s := range in {
		n, ok := normalizeGenderAliases[strings.ToLower(strings.TrimSpace(s))]
		if !ok || n == "unknown" {
			continue
		}
		set[n] = struct{}{}
	}
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, 2)
	for _, g := range []string{"male", "female"} {
		if _, ok := set[g]; ok {
			out = append(out, g)
		}
	}
	return out
}

// MetaGenderCodes maps canonical gender names to Meta's gender targeting ints.
func MetaGenderCodes(genders []string) []int {
	if len(genders) == 0 {
		return nil
	}
	codes := make([]int, 0, len(genders))
	for _, g := range genders {
		switch g {
		case "male":
			codes = append(codes, 1)
		case "female":
			codes = append(codes, 2)
		}
	}
	if len(codes) == 2 {
		// both male and female = everyone; omit so Meta targets all.
		return nil
	}
	return codes
}

// TikTokGenderCodes maps canonical gender values to TikTok's gender codes
// (1 = male, 2 = female; omit to target all).
func TikTokGenderCodes(genders []string) []int {
	return MetaGenderCodes(genders)
}

// BidStrategy constants.
const (
	BidStrategyLowestCostWithoutCap = "LOWEST_COST_WITHOUT_CAP"
	BidStrategyLowestCost           = "LOWEST_COST"
	BidStrategyCostCap              = "COST_CAP"
	BidStrategyRoasGoal             = "ROAS_GOAL"
)

// PacingType constants.
const (
	PacingStandard    = "standard"
	PacingAccelerated = "accelerated"
)

// FrequencyCapTimeUnit constants.
const (
	FreqCapHour = "hour"
	FreqCapDay  = "day"
	FreqCapWeek = "week"
)

// FieldProvenance tracks who set a campaign field value.
type FieldProvenance string

const (
	ProvenanceUser    FieldProvenance = "user"
	ProvenanceOma     FieldProvenance = "oma"
	ProvenanceDefault FieldProvenance = "default"
)

// VariantSpec pairs one targeting spec with one creative. Multiple variants let
// one campaign compare performance across audiences or ad copy against one budget.
type VariantSpec struct {
	Targeting map[string]any `json:"targeting"`
	Creative  *CreativeSpec  `json:"creative"`
}

// CampaignSpec is the single internal description of a campaign that is fanned
// out to every selected platform. Budgets are in major currency units.
// Targeting and Creative live under Variants so a future multi-variant UI
// (e.g. "Ikeja vs Lekki") slots in without a migration.
type CampaignSpec struct {
	Name             string
	Objective        CampaignObjective
	DailyBudget      float64
	Currency         string
	StartDate        time.Time
	EndDate          time.Time
	CTA              string
	BidStrategy      string
	BidCap           float64
	PacingType       string
	FrequencyCap     int
	FrequencyCapUnit string
	Format           string
	Variants         []VariantSpec

	// Deprecated, kept for backwards compat with existing platform adapters
	// during migration. Prefer Variants[0].Targeting / Variants[0].Creative.
	Targeting map[string]any
	Creative  *CreativeSpec
}

// FirstVariantTargeting returns the targeting for the first variant, falling back
// to the top-level Targeting for backwards compat.
func (s CampaignSpec) FirstVariantTargeting() map[string]any {
	if len(s.Variants) > 0 && s.Variants[0].Targeting != nil {
		return s.Variants[0].Targeting
	}
	return s.Targeting
}

// FirstVariantCreative returns the creative for the first variant, falling back
// to the top-level Creative for backwards compat.
func (s CampaignSpec) FirstVariantCreative() *CreativeSpec {
	if len(s.Variants) > 0 && s.Variants[0].Creative != nil {
		return s.Variants[0].Creative
	}
	return s.Creative
}

// ForecastSpec is the input for a reach/spend estimate. It carries only the
// fields that affect delivery (no name, no creative, no campaign ID).
type ForecastSpec struct {
	Platform    string         `json:"platform"`
	AdAccountID string         `json:"ad_account_id"`
	Objective   string         `json:"objective"`
	DailyBudget float64        `json:"daily_budget"`
	Currency    string         `json:"currency"`
	Targeting   map[string]any `json:"targeting"`
	BidStrategy string         `json:"bid_strategy,omitempty"`
	BidCap      float64        `json:"bid_cap,omitempty"`
	StartDate   string         `json:"start_date,omitempty"`
	EndDate     string         `json:"end_date,omitempty"`
}

// ForecastResult is the estimated reach/spend for a draft targeting spec.
type ForecastResult struct {
	EstimatedReach       int64   `json:"estimated_reach"`
	EstimatedImpressions int64   `json:"estimated_impressions"`
	EstimatedSpend       float64 `json:"estimated_spend"`
	EstimatedCPM         float64 `json:"estimated_cpm"`
	EstimatedClicks      int64   `json:"estimated_clicks"`
	Currency             string  `json:"currency"`
}

// CampaignForecaster provides delivery estimates for an unsaved targeting spec.
type CampaignForecaster interface {
	Platform() string
	EstimateDelivery(ctx context.Context, accessToken string, account PlatformAccount, spec ForecastSpec) (*ForecastResult, error)
}

// PlatformAccount carries the account-level identifiers an adapter needs.
type PlatformAccount struct {
	AccountID string // e.g. Meta act_<id>
	PageID    string // required to build ad creatives
	PixelID   string // required for conversion objectives
}

// DeliverableState is the set of platform object IDs that make up a fully
// deliverable campaign. Empty fields mean "not created yet" and let
// EnsureDeliverable resume a partially-built tree idempotently.
type DeliverableState struct {
	CampaignID string
	AdSetID    string
	CreativeID string
	AdID       string
}

// CreatedCampaign is the result of a single campaign-object creation step.
type CreatedCampaign struct {
	ExternalID string
	Status     string
}

// CampaignCreator is the per-platform adapter contract. Every platform takes the
// same generic CampaignSpec and returns the same DeliverableState, so the
// orchestration loop never changes when a new platform is added.
type CampaignCreator interface {
	// Platform returns the platform key (e.g. "meta") this adapter handles.
	Platform() string

	// EnsureDeliverable creates the full deliverable tree (campaign -> ad set ->
	// creative -> ad) in a paused/non-spending state. It is idempotent: any step
	// whose ID is already present in `have` is skipped. It returns the best-known
	// state even on error, so the caller can persist partial progress and resume.
	EnsureDeliverable(ctx context.Context, accessToken string, account PlatformAccount, spec CampaignSpec, have DeliverableState) (DeliverableState, error)
}

// CampaignManager mutates already-created campaigns (pause/resume, budget). Kept
// separate so the orchestration loop stays the same for every platform.
type CampaignManager interface {
	Platform() string

	// SetStatus flips a campaign between ACTIVE and PAUSED on the platform.
	SetStatus(ctx context.Context, accessToken, accountID, externalCampaignID, status string) error

	// UpdateAdSetBudget sets the daily budget (major currency units) on an ad set.
	UpdateAdSetBudget(ctx context.Context, accessToken, accountID, externalAdSetID string, dailyBudget float64) error
}

// CampaignAdapter is the full per-platform contract: create + manage. A single
// adapter (e.g. *meta.Client) satisfies it, and TikTok/Google plug in the same way.
type CampaignAdapter interface {
	CampaignCreator
	CampaignManager
}

// MediaUploader is the optional per-platform contract for ingesting creative
// bytes directly and returning a platform media id (e.g. Meta image_hash /
// video_id, TikTok image_id / video_id). Adapters that ingest hosted media by
// URL (the current Meta/TikTok path) do not need to implement it.
type MediaUploader interface {
	Platform() string
	UploadMedia(ctx context.Context, accessToken string, account PlatformAccount, kind string, data []byte, contentType string) (platformMediaID string, err error)
}

// NormalizedCampaign is a platform-agnostic campaign as read back during sync.
type NormalizedCampaign struct {
	ExternalID  string
	Name        string
	Objective   string
	Status      string
	DailyBudget float64
}

// CampaignDataSource reads a platform's campaigns and insights for syncing into
// the common snapshot shape. Every platform implements it the same way.
type CampaignDataSource interface {
	Platform() string
	FetchCampaigns(ctx context.Context, accessToken, accountID string) ([]NormalizedCampaign, error)
	FetchInsights(ctx context.Context, accessToken, accountID, campaignID, since, until string) ([]NormalizedInsight, error)
}
