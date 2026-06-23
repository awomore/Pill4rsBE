package config

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	DatabaseURL        string
	JWTSecret          string
	AnthropicAPIKey    string
	MetaAppID          string
	MetaAppSecret      string
	MetaRedirectURI    string
	TokenEncryptionKey string
	FrontendOrigin     string
	Port               string
	GoogleClientID     string
	GoogleClientSecret string
	GoogleRedirectURI  string
	RedisURL           string
}

func Load() (*Config, error) {
	godotenv.Load()

	cfg := &Config{
		DatabaseURL:        os.Getenv("DATABASE_URL"),
		JWTSecret:          os.Getenv("JWT_SECRET"),
		AnthropicAPIKey:    os.Getenv("ANTHROPIC_API_KEY"),
		MetaAppID:          os.Getenv("META_APP_ID"),
		MetaAppSecret:      os.Getenv("META_APP_SECRET"),
		MetaRedirectURI:    os.Getenv("META_REDIRECT_URI"),
		TokenEncryptionKey: os.Getenv("TOKEN_ENCRYPTION_KEY"),
		FrontendOrigin:     os.Getenv("FRONTEND_ORIGIN"),
		Port:               os.Getenv("PORT"),
		GoogleClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		GoogleRedirectURI:  os.Getenv("GOOGLE_REDIRECT_URI"),
		RedisURL:           os.Getenv("REDIS_URL"),
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
