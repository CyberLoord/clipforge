package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

type Config struct {
	Address        string
	DatabaseURL    string
	DataDir        string
	WebDir         string
	Migrations     string
	Mode           string
	APIKey         string
	BaseURL        string
	Model          string
	FFmpeg         string
	FFprobe        string
	Workers        int
	MaxAttempts    int
	MaxUploadBytes int64
	MaxDuration    float64
	StageTimeout   time.Duration
	LeaseDuration  time.Duration
}

func Load() (Config, error) {
	c := Config{
		Address:     env("ADDR", "127.0.0.1:8080"),
		DatabaseURL: env("DATABASE_URL", "postgres://clipforge:clipforge@localhost:5432/clipforge?sslmode=disable"),
		DataDir:     env("DATA_DIR", "data"),
		WebDir:      env("WEB_DIR", "web/dist"),
		Migrations:  env("MIGRATIONS_DIR", "migrations"),
		Mode:        env("AI_MODE", "demo"),
		APIKey:      os.Getenv("OPENAI_API_KEY"),
		BaseURL:     env("OPENAI_BASE_URL", "https://api.openai.com/v1"),
		Model:       env("OPENAI_SELECTION_MODEL", "gpt-4.1-mini"),
		FFmpeg:      env("FFMPEG_PATH", "ffmpeg"),
		FFprobe:     env("FFPROBE_PATH", "ffprobe"),
	}
	var err error
	if c.Workers, err = integer("WORKERS", 2, 1, 32); err != nil {
		return c, err
	}
	if c.MaxAttempts, err = integer("MAX_ATTEMPTS", 3, 1, 10); err != nil {
		return c, err
	}
	mb, err := integer("MAX_UPLOAD_MB", 300, 1, 4096)
	if err != nil {
		return c, err
	}
	c.MaxUploadBytes = int64(mb) * 1024 * 1024
	seconds, err := integer("MAX_DURATION_SECONDS", 1800, 20, 86400)
	if err != nil {
		return c, err
	}
	c.MaxDuration = float64(seconds)
	if c.StageTimeout, err = time.ParseDuration(env("STAGE_TIMEOUT", "15m")); err != nil || c.StageTimeout <= 0 {
		return c, fmt.Errorf("invalid STAGE_TIMEOUT")
	}
	if c.LeaseDuration, err = time.ParseDuration(env("LEASE_DURATION", "30s")); err != nil || c.LeaseDuration < 3*time.Second {
		return c, fmt.Errorf("LEASE_DURATION must be at least 3s")
	}
	if c.Mode != "demo" && c.Mode != "openai" {
		return c, fmt.Errorf("AI_MODE must be demo or openai")
	}
	if c.Mode == "openai" && c.APIKey == "" {
		return c, fmt.Errorf("OPENAI_API_KEY is required in openai mode")
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1"))) {
		return c, fmt.Errorf("OPENAI_BASE_URL must use HTTPS")
	}
	c.DataDir, err = filepath.Abs(c.DataDir)
	return c, err
}
func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func integer(key string, fallback, min, max int) (int, error) {
	v, err := strconv.Atoi(env(key, strconv.Itoa(fallback)))
	if err != nil || v < min || v > max {
		return 0, fmt.Errorf("%s must be between %d and %d", key, min, max)
	}
	return v, nil
}
