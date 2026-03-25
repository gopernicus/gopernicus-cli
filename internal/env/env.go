// Package env provides environment variable utilities for gopernicus CLI commands.
//
// Usage:
//
//	cfg := env.New(".env", projectRoot)
//	dbURL, err := cfg.Require("DATABASE_URL")
package env

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/joho/godotenv"
)

// Config holds resolved env settings.
type Config struct{}

// New creates a Config, loading the .env file from the project root as a side effect.
func New(envFile, projectRoot string) *Config {
	if envFile == "" {
		envFile = ".env"
	}

	// Load .env — silently ignore if not present.
	_ = godotenv.Load(filepath.Join(projectRoot, envFile))

	return &Config{}
}

// Get returns the value of an environment variable.
func (c *Config) Get(key string) string {
	return os.Getenv(key)
}

// GetOrDefault returns the value or fallback if the variable is unset.
func (c *Config) GetOrDefault(key, fallback string) string {
	if v := c.Get(key); v != "" {
		return v
	}
	return fallback
}

// Require returns the value or an error if the variable is unset.
func (c *Config) Require(key string) (string, error) {
	v := c.Get(key)
	if v == "" {
		return "", fmt.Errorf("required environment variable %s is not set", key)
	}
	return v, nil
}
