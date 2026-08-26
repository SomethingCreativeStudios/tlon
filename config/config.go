// Package config loads standalone-server settings from environment variables.
package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"time"
)

type Config struct {
	DatabaseURL       string
	PublicURL         string
	ListenAddress     string
	CursorSecret      string
	AuthorizationMode string
	DefaultLimit      int
	MaximumLimit      int
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
	LogLevel          string
}

func Load() (Config, error) {
	c := Config{
		DatabaseURL:       os.Getenv("TLON_DATABASE_URL"),
		PublicURL:         getenv("TLON_PUBLIC_URL", "http://localhost:8080"),
		ListenAddress:     getenv("TLON_LISTEN_ADDR", ":8080"),
		CursorSecret:      os.Getenv("TLON_CURSOR_SECRET"),
		AuthorizationMode: getenv("TLON_AUTH_MODE", "deny"),
		DefaultLimit:      10,
		MaximumLimit:      1000,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		ShutdownTimeout:   15 * time.Second,
		LogLevel:          getenv("TLON_LOG_LEVEL", "info"),
	}
	var err error
	if c.DefaultLimit, err = intEnv("TLON_DEFAULT_LIMIT", c.DefaultLimit); err != nil {
		return Config{}, err
	}
	if c.MaximumLimit, err = intEnv("TLON_MAX_LIMIT", c.MaximumLimit); err != nil {
		return Config{}, err
	}
	if c.ReadTimeout, err = durationEnv("TLON_READ_TIMEOUT", c.ReadTimeout); err != nil {
		return Config{}, err
	}
	if c.WriteTimeout, err = durationEnv("TLON_WRITE_TIMEOUT", c.WriteTimeout); err != nil {
		return Config{}, err
	}
	if c.IdleTimeout, err = durationEnv("TLON_IDLE_TIMEOUT", c.IdleTimeout); err != nil {
		return Config{}, err
	}
	if c.ShutdownTimeout, err = durationEnv("TLON_SHUTDOWN_TIMEOUT", c.ShutdownTimeout); err != nil {
		return Config{}, err
	}
	if c.DatabaseURL == "" {
		return Config{}, fmt.Errorf("TLON_DATABASE_URL is required")
	}
	if len(c.CursorSecret) < 32 {
		return Config{}, fmt.Errorf("TLON_CURSOR_SECRET must contain at least 32 bytes")
	}
	if c.AuthorizationMode != "deny" && c.AuthorizationMode != "external" {
		return Config{}, fmt.Errorf("TLON_AUTH_MODE must be deny or external")
	}
	if c.DefaultLimit < 1 || c.MaximumLimit < 1 || c.DefaultLimit > c.MaximumLimit || c.MaximumLimit > 10000 {
		return Config{}, fmt.Errorf("invalid limit configuration")
	}
	publicURL, err := url.Parse(c.PublicURL)
	if err != nil || (publicURL.Scheme != "http" && publicURL.Scheme != "https") || publicURL.Host == "" {
		return Config{}, fmt.Errorf("TLON_PUBLIC_URL must be an absolute HTTP(S) URL")
	}
	if c.ReadTimeout <= 0 || c.WriteTimeout <= 0 || c.IdleTimeout <= 0 || c.ShutdownTimeout <= 0 {
		return Config{}, fmt.Errorf("timeouts must be positive")
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return Config{}, fmt.Errorf("TLON_LOG_LEVEL must be debug, info, warn, or error")
	}
	return c, nil
}

func getenv(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
func intEnv(name string, fallback int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	return n, nil
}
func durationEnv(name string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	return d, nil
}
