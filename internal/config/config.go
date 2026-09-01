// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

// Package config reads machine-level settings from ~/.config/skillmod/config.toml.
// Platform selection is a machine or user preference and belongs here rather than in SKILL.mod.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/huija/skillmod/internal/i18n"
	"github.com/pelletier/go-toml/v2"
)

// Config contains machine-level settings.
type Config struct {
	// Agents lists installation target platforms; empty means ["agents"].
	Agents []string `toml:"agents"`
	// KnownSources lists repositories used by init for ls-remote matching and by list for discovery.
	KnownSources []string `toml:"known_sources"`
}

// Default returns the default configuration.
func Default() *Config {
	return &Config{Agents: []string{"agents"}}
}

// Path returns the configuration file path.
func Path() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "skillmod", "config.toml"), nil
}

// Load reads the configuration, returning defaults when the file does not exist.
func Load() (*Config, error) {
	p, err := Path()
	if err != nil {
		return Default(), nil
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf(i18n.Text("parse %s: %w"), p, err)
	}
	if len(cfg.Agents) == 0 {
		cfg.Agents = Default().Agents
	}
	return &cfg, nil
}
