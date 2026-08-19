package config

import (
	"fmt"
	"os"
)

type Config struct {
	HTTPAddr string
	LogLevel string
}

func Load() (Config, error) {
	cfg := Config{
		HTTPAddr: envOrDefault("HTTP_ADDR", ":8080"),
		LogLevel: envOrDefault("LOG_LEVEL", "info"),
	}

	if cfg.HTTPAddr == "" {
		return Config{}, fmt.Errorf("HTTP_ADDR must not be empty")
	}

	return cfg, nil
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
