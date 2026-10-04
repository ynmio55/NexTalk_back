package config

import (
	"os"
)

type Config struct {
	Addr        string
	DatabaseURL string
	JWTSecret   string
	CORSOrigin  string
	InviteCode  string
}

func Load() Config {
	return Config{
		Addr:        env("APP_ADDR", ":8080"),
		DatabaseURL: env("DATABASE_URL", ""),
		JWTSecret:   env("JWT_SECRET", "dev-only-change-me"),
		CORSOrigin:  env("CORS_ORIGIN", "http://localhost:5173"),
		InviteCode:  env("INVITE_CODE", ""),
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
