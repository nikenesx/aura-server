package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	defaultMaxUploadBytes int64 = 512 << 20
	defaultASRTimeout           = 30 * time.Minute
	defaultLLMTimeout           = 5 * time.Minute
	defaultSessionTTL           = 30 * 24 * time.Hour
	defaultUploadTimeout        = 30 * time.Minute
)

type Config struct {
	HTTPAddr       string
	LogLevel       string
	ASRURL         string
	MaxUploadBytes int64
	ASRTimeout     time.Duration
	// UploadTimeout is how long one upload or audio download may run.
	UploadTimeout time.Duration
	// LLMURL is the OpenAI-compatible server for appointment cards; empty
	// disables cards.
	LLMURL     string
	LLMModel   string
	LLMTimeout time.Duration

	// DataDir holds the SQLite database and the recordings unless DBPath or
	// AudioDir point elsewhere.
	DataDir  string
	DBPath   string
	AudioDir string

	SessionTTL time.Duration
	// The bootstrap account is created only when there are no users yet.
	BootstrapUser     string
	BootstrapPassword string
}

func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:          envOrDefault("HTTP_ADDR", ":8080"),
		LogLevel:          envOrDefault("LOG_LEVEL", "info"),
		ASRURL:            strings.TrimRight(envOrDefault("ASR_URL", "http://127.0.0.1:8090"), "/"),
		MaxUploadBytes:    defaultMaxUploadBytes,
		LLMURL:            strings.TrimRight(os.Getenv("LLM_URL"), "/"),
		LLMModel:          envOrDefault("LLM_MODEL", "t-lite"),
		DataDir:           envOrDefault("DATA_DIR", "data"),
		BootstrapUser:     envOrDefault("AUTH_BOOTSTRAP_USER", "admin"),
		BootstrapPassword: envOrDefault("AUTH_BOOTSTRAP_PASSWORD", "1234"),
	}
	cfg.DBPath = envOrDefault("DB_PATH", filepath.Join(cfg.DataDir, "aura.db"))
	cfg.AudioDir = envOrDefault("AUDIO_DIR", filepath.Join(cfg.DataDir, "audio"))

	if cfg.HTTPAddr == "" {
		return Config{}, fmt.Errorf("HTTP_ADDR must not be empty")
	}
	if cfg.ASRURL == "" {
		return Config{}, fmt.Errorf("ASR_URL must not be empty")
	}
	if cfg.BootstrapUser == "" || cfg.BootstrapPassword == "" {
		return Config{}, fmt.Errorf("AUTH_BOOTSTRAP_USER and AUTH_BOOTSTRAP_PASSWORD must not be empty")
	}

	if raw := os.Getenv("MAX_UPLOAD_BYTES"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("MAX_UPLOAD_BYTES must be a positive integer")
		}
		cfg.MaxUploadBytes = n
	}

	durations := []struct {
		env string
		dst *time.Duration
		def time.Duration
	}{
		{"ASR_TIMEOUT", &cfg.ASRTimeout, defaultASRTimeout},
		{"LLM_TIMEOUT", &cfg.LLMTimeout, defaultLLMTimeout},
		{"SESSION_TTL", &cfg.SessionTTL, defaultSessionTTL},
		{"UPLOAD_TIMEOUT", &cfg.UploadTimeout, defaultUploadTimeout},
	}
	for _, d := range durations {
		*d.dst = d.def
		raw := os.Getenv(d.env)
		if raw == "" {
			continue
		}
		v, err := time.ParseDuration(raw)
		if err != nil || v <= 0 {
			return Config{}, fmt.Errorf("%s must be a positive duration", d.env)
		}
		*d.dst = v
	}

	return cfg, nil
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
