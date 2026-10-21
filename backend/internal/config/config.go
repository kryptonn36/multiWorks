package config

import (
	"fmt"
	"os"
)

type Config struct {
	Port     string
	DatabaseURL string
}

func Load() (Config, error) {
	cfg := Config{
		Port:     getEnv("PORT", ":8080"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
	}

	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL environment variable is required")
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	return value
}
