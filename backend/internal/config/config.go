package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	AssetStorageRoot    string
	AssetMaxUploadBytes int64
	AssetMaxPixels      int64
	AgentMaxOps         int
	AgentTimeout        time.Duration
	Port                string
	DatabaseURL         string
	JWTSecret           string
}

func Load() (Config, error) {
	cfg := Config{
		AssetStorageRoot: getEnv("ASSET_STORAGE_ROOT", "./data/assets"),
		Port:             getEnv("PORT", ":8080"),
		DatabaseURL:      os.Getenv("DATABASE_URL"),
		JWTSecret:        os.Getenv("JWT_SECRET"),
	}

	if strings.TrimSpace(cfg.AssetStorageRoot) == "" || strings.ContainsRune(cfg.AssetStorageRoot, 0) {
		return Config{}, fmt.Errorf("ASSET_STORAGE_ROOT must be a valid non-empty path")
	}
	if root, ok := os.LookupEnv("ASSET_STORAGE_ROOT"); ok && strings.TrimSpace(root) == "" {
		return Config{}, fmt.Errorf("ASSET_STORAGE_ROOT must be a valid non-empty path")
	}
	var err error
	cfg.AssetMaxUploadBytes, err = positiveInteger("ASSET_MAX_UPLOAD_BYTES", 10485760, 1073741824)
	if err != nil {
		return Config{}, err
	}
	cfg.AssetMaxPixels, err = positiveInteger("ASSET_MAX_PIXELS", 25000000, 100000000)
	if err != nil {
		return Config{}, err
	}
	maxOps, err := positiveInteger("AGENT_MAX_OPS", 20, 100)
	if err != nil {
		return Config{}, err
	}
	cfg.AgentMaxOps = int(maxOps)
	timeout, err := positiveInteger("AGENT_TIMEOUT_SECONDS", 15, 300)
	if err != nil {
		return Config{}, err
	}
	cfg.AgentTimeout = time.Duration(timeout) * time.Second

	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL environment variable is required")
	}
	if strings.TrimSpace(cfg.JWTSecret) == "" {
		return Config{}, fmt.Errorf("JWT_SECRET environment variable is required")
	}

	if len(cfg.JWTSecret) < 32 {
		return Config{}, fmt.Errorf("JWT_SECRET must be at least 32 bytes")
	}

	if cfg.Port == "" {
		return Config{}, fmt.Errorf("APP_ADDR or PORT must not be empty")
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

func positiveInteger(key string, fallback, maximum int64) (int64, error) {
	raw, ok := os.LookupEnv(key)
	if !ok {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 1 || value > maximum {
		return 0, fmt.Errorf("%s must be an integer between 1 and %d", key, maximum)
	}
	return value, nil
}
