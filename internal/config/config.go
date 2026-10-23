package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	DatabaseURL     string
	JWTSecret       string
	AnthropicAPIKey string
	MetaAppID       string
	MetaAppSecret   string
	MetaRedirectURI string
	// Pill4rs-owned Business Manager credentials for managed ad accounts.
	MetaBusinessID         string
	MetaManagedAccessToken string
	TokenEncryptionKey     string
	FrontendOrigin         string
	Port                   string
	GoogleClientID         string
	GoogleClientSecret     string
	GoogleRedirectURI      string
	TikTokAppID            string
	TikTokAppSecret        string
	TikTokRedirectURI      string
	// Pill4rs-owned TikTok Business Center credentials for managed accounts.
	TikTokBusinessCenterID   string
	TikTokManagedAccessToken string
	GoogleAdsDeveloperToken  string
	GoogleAdsLoginCustomerID string
	GoogleAdsRedirectURI     string
	// GoogleAdsMCCRefreshToken is the Pill4rs-owned manager account refresh
	// token used to create and fund managed customer accounts.
	GoogleAdsMCCRefreshToken string
	// DV360 (Display & Video 360) — DSP buying backbone. Reuses the Google OAuth
	// client with the display-video + doubleclickbidmanager scopes.
	ProgrammaticRedirectURI string
	// Pill4rs-owned DSP partner credentials for managed advertiser creation.
	DV360PartnerID           string
	DV360BillingProfileID    string
	DV360ManagedRefreshToken string
	RedisURL                 string
	// Media storage.
	StorageDriver      string
	MediaDir           string
	MediaPublicBaseURL string
	// Billing.
	CommissionRateBps int
	// FXNGNPerUSD converts NGN wallet top-ups to USD for managed-platform
	// spend caps/budgets when the account currency differs. 0 disables FX.
	FXNGNPerUSD float64
	// PlatformAdminEmails bootstraps Pill4rs platform admins.
	PlatformAdminEmails      []string
	FlutterwaveSecretKey     string
	FlutterwaveWebhookSecret string
	FlutterwaveBaseURL       string
	// Zernio — unified ads provider for the networks we don't build natively.
	ZernioAPIKey  string
	ZernioBaseURL string
}

func Load() (*Config, error) {
	godotenv.Load()

	cfg := &Config{
		DatabaseURL:              os.Getenv("DATABASE_URL"),
		JWTSecret:                os.Getenv("JWT_SECRET"),
		AnthropicAPIKey:          os.Getenv("ANTHROPIC_API_KEY"),
		MetaAppID:                os.Getenv("META_APP_ID"),
		MetaAppSecret:            os.Getenv("META_APP_SECRET"),
		MetaRedirectURI:          os.Getenv("META_REDIRECT_URI"),
		MetaBusinessID:           os.Getenv("META_BUSINESS_ID"),
		MetaManagedAccessToken:   os.Getenv("META_MANAGED_ACCESS_TOKEN"),
		TokenEncryptionKey:       os.Getenv("TOKEN_ENCRYPTION_KEY"),
		FrontendOrigin:           os.Getenv("FRONTEND_ORIGIN"),
		Port:                     os.Getenv("PORT"),
		GoogleClientID:           os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret:       os.Getenv("GOOGLE_CLIENT_SECRET"),
		GoogleRedirectURI:        os.Getenv("GOOGLE_REDIRECT_URI"),
		TikTokAppID:              os.Getenv("TIKTOK_APP_ID"),
		TikTokAppSecret:          os.Getenv("TIKTOK_APP_SECRET"),
		TikTokRedirectURI:        os.Getenv("TIKTOK_REDIRECT_URI"),
		TikTokBusinessCenterID:   os.Getenv("TIKTOK_BUSINESS_CENTER_ID"),
		TikTokManagedAccessToken: os.Getenv("TIKTOK_MANAGED_ACCESS_TOKEN"),
		GoogleAdsDeveloperToken:  os.Getenv("GOOGLE_ADS_DEVELOPER_TOKEN"),
		GoogleAdsLoginCustomerID: os.Getenv("GOOGLE_ADS_LOGIN_CUSTOMER_ID"),
		GoogleAdsRedirectURI:     os.Getenv("GOOGLE_ADS_REDIRECT_URI"),
		GoogleAdsMCCRefreshToken: os.Getenv("GOOGLE_ADS_MCC_REFRESH_TOKEN"),
		ProgrammaticRedirectURI:  envOr("PROGRAMMATIC_REDIRECT_URI", os.Getenv("DV360_REDIRECT_URI")),
		RedisURL:                 os.Getenv("REDIS_URL"),
		StorageDriver:            envOr("STORAGE_DRIVER", "local"),
		MediaDir:                 envOr("MEDIA_DIR", "media"),
		MediaPublicBaseURL:       os.Getenv("MEDIA_PUBLIC_BASE_URL"),
		CommissionRateBps:        envIntOr("COMMISSION_RATE_BPS", 1000),
		FXNGNPerUSD:              envFloatOr("FX_NGN_PER_USD", 0),
		PlatformAdminEmails:      envCSV("PILL4RS_ADMIN_EMAILS"),
		FlutterwaveSecretKey:     os.Getenv("FLUTTERWAVE_SECRET_KEY"),
		FlutterwaveWebhookSecret: os.Getenv("FLUTTERWAVE_WEBHOOK_SECRET_HASH"),
		FlutterwaveBaseURL:       envOr("FLUTTERWAVE_BASE_URL", "https://api.flutterwave.com/v3"),
		ZernioAPIKey:             os.Getenv("ZERNIO_API_KEY"),
		ZernioBaseURL:            envOr("ZERNIO_BASE_URL", "https://zernio.com/api"),
	}

	if cfg.MediaPublicBaseURL == "" {
		cfg.MediaPublicBaseURL = fmt.Sprintf("http://localhost:%s", cfg.Port)
	}

	missing := []string{}
	if cfg.DatabaseURL == "" {
		missing = append(missing, "DATABASE_URL")
	}
	if cfg.JWTSecret == "" {
		missing = append(missing, "JWT_SECRET")
	}
	if cfg.AnthropicAPIKey == "" {
		missing = append(missing, "ANTHROPIC_API_KEY")
	}
	if cfg.MetaAppID == "" {
		missing = append(missing, "META_APP_ID")
	}
	if cfg.MetaAppSecret == "" {
		missing = append(missing, "META_APP_SECRET")
	}
	if cfg.MetaRedirectURI == "" {
		missing = append(missing, "META_REDIRECT_URI")
	}
	if cfg.TokenEncryptionKey == "" {
		missing = append(missing, "TOKEN_ENCRYPTION_KEY")
	}
	if cfg.FrontendOrigin == "" {
		missing = append(missing, "FRONTEND_ORIGIN")
	}
	if cfg.Port == "" {
		missing = append(missing, "PORT")
	}

	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required environment variables: %v", missing)
	}

	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envCSV(key string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(strings.ToLower(p)); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func envFloatOr(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil {
			return n
		}
	}
	return fallback
}

func envIntOr(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}
