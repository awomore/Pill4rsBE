package config

import (
	"fmt"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	DatabaseURL              string
	JWTSecret                string
	AnthropicAPIKey          string
	MetaAppID                string
	MetaAppSecret            string
	MetaRedirectURI          string
	TokenEncryptionKey       string
	FrontendOrigin           string
	Port                     string
	GoogleClientID           string
	GoogleClientSecret       string
	GoogleRedirectURI        string
	TikTokAppID              string
	TikTokAppSecret          string
	TikTokRedirectURI        string
	GoogleAdsDeveloperToken  string
	GoogleAdsLoginCustomerID string
	GoogleAdsRedirectURI     string
	RedisURL                 string
	// Media storage.
	StorageDriver      string
	MediaDir           string
	MediaPublicBaseURL string
	// Billing.
	CommissionRateBps        int
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
		TokenEncryptionKey:       os.Getenv("TOKEN_ENCRYPTION_KEY"),
		FrontendOrigin:           os.Getenv("FRONTEND_ORIGIN"),
		Port:                     os.Getenv("PORT"),
		GoogleClientID:           os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret:       os.Getenv("GOOGLE_CLIENT_SECRET"),
		GoogleRedirectURI:        os.Getenv("GOOGLE_REDIRECT_URI"),
		TikTokAppID:              os.Getenv("TIKTOK_APP_ID"),
		TikTokAppSecret:          os.Getenv("TIKTOK_APP_SECRET"),
		TikTokRedirectURI:        os.Getenv("TIKTOK_REDIRECT_URI"),
		GoogleAdsDeveloperToken:  os.Getenv("GOOGLE_ADS_DEVELOPER_TOKEN"),
		GoogleAdsLoginCustomerID: os.Getenv("GOOGLE_ADS_LOGIN_CUSTOMER_ID"),
		GoogleAdsRedirectURI:     os.Getenv("GOOGLE_ADS_REDIRECT_URI"),
		RedisURL:                 os.Getenv("REDIS_URL"),
		StorageDriver:            envOr("STORAGE_DRIVER", "local"),
		MediaDir:                 envOr("MEDIA_DIR", "media"),
		MediaPublicBaseURL:       os.Getenv("MEDIA_PUBLIC_BASE_URL"),
		CommissionRateBps:        envIntOr("COMMISSION_RATE_BPS", 1000),
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

func envIntOr(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}
