package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

func validEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://fixture/test")
	t.Setenv("JWT_SECRET", strings.Repeat("x", 32))
	for _, key := range []string{"ASSET_STORAGE_ROOT", "ASSET_MAX_UPLOAD_BYTES", "ASSET_MAX_PIXELS", "AGENT_MAX_OPS", "AGENT_TIMEOUT_SECONDS"} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
}
func TestDefaultsAndOverrides(t *testing.T) {
	validEnvironment(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AssetStorageRoot != "./data/assets" || cfg.AssetMaxUploadBytes != 10485760 || cfg.AssetMaxPixels != 25000000 || cfg.AgentMaxOps != 20 || cfg.AgentTimeout != 15*time.Second {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	t.Setenv("ASSET_STORAGE_ROOT", "/tmp/test-assets")
	t.Setenv("ASSET_MAX_UPLOAD_BYTES", "2048")
	t.Setenv("ASSET_MAX_PIXELS", "10000")
	t.Setenv("AGENT_MAX_OPS", "30")
	t.Setenv("AGENT_TIMEOUT_SECONDS", "60")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AssetStorageRoot != "/tmp/test-assets" || cfg.AssetMaxUploadBytes != 2048 || cfg.AssetMaxPixels != 10000 || cfg.AgentMaxOps != 30 || cfg.AgentTimeout != time.Minute {
		t.Fatal("overrides ignored")
	}
}
func TestInvalidLimitsFailStartup(t *testing.T) {
	for _, key := range []string{"ASSET_MAX_UPLOAD_BYTES", "ASSET_MAX_PIXELS", "AGENT_MAX_OPS", "AGENT_TIMEOUT_SECONDS"} {
		for _, value := range []string{"", "0", "-1", "1.5", "abc", " 20 ", "99999999999999999999999"} {
			t.Run(key+"/"+value, func(t *testing.T) {
				validEnvironment(t)
				t.Setenv(key, value)
				_, err := Load()
				if err == nil || !strings.Contains(err.Error(), key) {
					t.Fatalf("invalid %s accepted: %v", key, err)
				}
			})
		}
	}
	for key, value := range map[string]string{"ASSET_MAX_UPLOAD_BYTES": "1073741825", "ASSET_MAX_PIXELS": "100000001", "AGENT_MAX_OPS": "101", "AGENT_TIMEOUT_SECONDS": "301", "ASSET_STORAGE_ROOT": " "} {
		t.Run(key+"/range", func(t *testing.T) {
			validEnvironment(t)
			t.Setenv(key, value)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("out-of-range %s accepted", key)
			}
		})
	}
}
