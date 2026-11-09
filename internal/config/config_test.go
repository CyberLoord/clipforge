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
	for _, name := range []string{"APP_ROLE", "KAFKA_BROKERS", "KAFKA_GROUP_PREFIX", "KAFKA_PARTITIONS", "KAFKA_REPLICATION_FACTOR", "RENDER_WORKERS", "ADDR", "DATABASE_URL", "DATA_DIR", "WEB_DIR", "MIGRATIONS_DIR", "AI_MODE", "OPENAI_API_KEY", "OPENAI_BASE_URL", "OPENAI_SELECTION_MODEL", "OPENAI_VISION_MODEL", "FFMPEG_PATH", "FFPROBE_PATH", "WORKERS", "MAX_ATTEMPTS", "MAX_UPLOAD_MB", "MAX_DURATION_SECONDS", "STAGE_TIMEOUT", "LEASE_DURATION"} {
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
	if cfg.APIKey != "test-key" || cfg.Workers != 3 || cfg.MaxDuration != 15 || cfg.Mode != "montage" || cfg.VisionModel != "gpt-5.6-terra" {
		t.Fatal("dotenv settings or environment precedence are incorrect")
	}
	if !reflect.DeepEqual(cfg.AvailableModes(), []string{"split", "montage", "openai", "demo"}) {
		t.Fatal("configured provider is unavailable")
	}
}

func TestLoadWithoutDotEnv(t *testing.T) {
	isolateConfig(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != "montage" || !reflect.DeepEqual(cfg.AvailableModes(), []string{"split", "demo"}) {
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

func TestKafkaConfiguration(t *testing.T) {
	isolateConfig(t)
	t.Setenv("APP_ROLE", "render")
	t.Setenv("KAFKA_BROKERS", "broker-a:9092, broker-b:9092")
	t.Setenv("KAFKA_PARTITIONS", "6")
	t.Setenv("RENDER_WORKERS", "3")
	cfg, err := Load()
	if err != nil || cfg.Role != "render" || cfg.KafkaPartitions != 6 || cfg.RenderWorkers != 3 || !reflect.DeepEqual(cfg.KafkaBrokers, []string{"broker-a:9092", "broker-b:9092"}) {
		t.Fatalf("invalid Kafka configuration: %+v %v", cfg, err)
	}
}

func TestInvalidKafkaConfiguration(t *testing.T) {
	for key, value := range map[string]string{"APP_ROLE": "unknown", "KAFKA_BROKERS": "localhost:9092,", "KAFKA_PARTITIONS": "0", "RENDER_WORKERS": "0", "KAFKA_GROUP_PREFIX": "two groups", "KAFKA_REPLICATION_FACTOR": "0"} {
		t.Run(key, func(t *testing.T) {
			isolateConfig(t)
			t.Setenv(key, value)
			if _, err := Load(); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
}
