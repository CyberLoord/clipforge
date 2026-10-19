package config

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func isolateConfig(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	for _, name := range []string{"ADDR", "DATABASE_URL", "DATA_DIR", "WEB_DIR", "MIGRATIONS_DIR", "AI_MODE", "OPENAI_API_KEY", "OPENAI_BASE_URL", "OPENAI_SELECTION_MODEL", "FFMPEG_PATH", "FFPROBE_PATH", "WORKERS", "MAX_ATTEMPTS", "MAX_UPLOAD_MB", "MAX_DURATION_SECONDS", "STAGE_TIMEOUT", "LEASE_DURATION"} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLoadDotEnv(t *testing.T) {
	isolateConfig(t)
	t.Setenv("WORKERS", "3")
	if err := os.WriteFile(".env", []byte("OPENAI_API_KEY=test-key\nWORKERS=4\nMAX_DURATION_SECONDS=15\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIKey != "test-key" || cfg.Workers != 3 || cfg.MaxDuration != 15 || cfg.Mode != "split" {
		t.Fatal("dotenv settings or environment precedence are incorrect")
	}
	if !reflect.DeepEqual(cfg.AvailableModes(), []string{"split", "openai", "demo"}) {
		t.Fatal("configured provider is unavailable")
	}
}

func TestLoadWithoutDotEnv(t *testing.T) {
	isolateConfig(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != "split" || !reflect.DeepEqual(cfg.AvailableModes(), []string{"split", "demo"}) {
		t.Fatal("unexpected default modes")
	}
}

func TestMalformedDotEnvDoesNotLeakValues(t *testing.T) {
	isolateConfig(t)
	if err := os.WriteFile(".env", []byte("OPENAI_API_KEY='private-value"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := Load()
	if err == nil || strings.Contains(err.Error(), "private-value") {
		t.Fatal("expected a sanitized configuration error")
	}
}
