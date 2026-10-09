package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultMaxUploadBytes int64 = 100 * 1024 * 1024
	defaultASRTimeout           = 30 * time.Minute
	defaultJobTTL               = 2 * time.Hour
	defaultLLMTimeout           = 5 * time.Minute
)

type Config struct {
	HTTPAddr       string
	LogLevel       string
	ASRURL         string
	MaxUploadBytes int64
	ASRTimeout     time.Duration
	JobTTL         time.Duration
	// LLMURL is the OpenAI-compatible server for appointment cards; empty
	// disables cards.
	LLMURL     string
	LLMModel   string
	LLMTimeout time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:       envOrDefault("HTTP_ADDR", ":8080"),
		LogLevel:       envOrDefault("LOG_LEVEL", "info"),
		ASRURL:         strings.TrimRight(envOrDefault("ASR_URL", "http://127.0.0.1:8090"), "/"),
		MaxUploadBytes: defaultMaxUploadBytes,
		ASRTimeout:     defaultASRTimeout,
		JobTTL:         defaultJobTTL,
		LLMURL:         strings.TrimRight(os.Getenv("LLM_URL"), "/"),
		LLMModel:       envOrDefault("LLM_MODEL", "t-lite"),
		LLMTimeout:     defaultLLMTimeout,
	}

	if raw := os.Getenv("LLM_TIMEOUT"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			return Config{}, fmt.Errorf("LLM_TIMEOUT must be a positive duration")
		}
		cfg.LLMTimeout = d
	}

	if cfg.HTTPAddr == "" {
		return Config{}, fmt.Errorf("HTTP_ADDR must not be empty")
	}
	if cfg.ASRURL == "" {
		return Config{}, fmt.Errorf("ASR_URL must not be empty")
	}

	if raw := os.Getenv("MAX_UPLOAD_BYTES"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("MAX_UPLOAD_BYTES must be a positive integer")
		}
		cfg.MaxUploadBytes = n
	}

	if raw := os.Getenv("ASR_TIMEOUT"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			return Config{}, fmt.Errorf("ASR_TIMEOUT must be a positive duration")
		}
		cfg.ASRTimeout = d
	}

	if raw := os.Getenv("JOB_TTL"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			return Config{}, fmt.Errorf("JOB_TTL must be a positive duration")
		}
		cfg.JobTTL = d
	}

	return cfg, nil
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
