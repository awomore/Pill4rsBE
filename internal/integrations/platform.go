package integrations

import (
	"context"
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
// platforms this lives on an ad-creative object below the campaign.
type CreativeSpec struct {
	PrimaryText string `json:"primary_text"`
	Headline    string `json:"headline"`
	Description string `json:"description"`
	LinkURL     string `json:"link_url"`
	ImageURL    string `json:"image_url"`
}

// CampaignSpec is the single internal description of a campaign that is fanned
// out to every selected platform. Budgets are in major currency units.
type CampaignSpec struct {
	Name        string
	Objective   CampaignObjective
	DailyBudget float64
	Currency    string
	StartDate   time.Time
	EndDate     time.Time
	CTA         string
	Targeting   map[string]any
	Creative    *CreativeSpec
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
