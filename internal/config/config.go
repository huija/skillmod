// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

// Package config reads machine-level settings from ~/.config/skillmod/config.toml.
// Installation directories are not a preference: skillmod manages one convention.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/huija/skillmod/internal/i18n"
	"github.com/huija/skillmod/internal/install"
	"github.com/pelletier/go-toml/v2"
)

// removedDefaultAgent is the adapter name the removed installation-platform
// setting accepted for the directory skillmod still manages. A configuration
// that only names it described exactly today's behavior, so it is tolerated.
const removedDefaultAgent = "agents"

// Config contains machine-level settings. Installation directories are not a
// setting: skillmod manages one convention, described by install.SkillsDirName.
type Config struct {
	// InstallMode is auto (default) or copy; it is not written to mod/lock.
	InstallMode install.Mode `toml:"install_mode"`
	// KnownSources lists repositories used by init for ls-remote matching and by list for discovery.
	KnownSources []string `toml:"known_sources"`
	// Agents is the removed installation-platform setting. It is read only to
	// reject a configuration that selected a directory skillmod no longer
	// manages: ignoring it would install somewhere other than the file says, and
	// silence is worse than a message naming the file and the offending entry.
	Agents []string `toml:"agents"`
}

// Default returns the default configuration.
func Default() *Config {
	return &Config{}
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
		return nil, fmt.Errorf(i18n.Text("config.parse"), p, err)
	}
	if err := install.ValidateMode(cfg.InstallMode); err != nil {
		return nil, err
	}
	if err := validateAgents(cfg.Agents, p); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// validateAgents rejects a configuration written for the release that selected
// installation platforms, since those directories are no longer managed.
func validateAgents(agents []string, configPath string) error {
	for _, agent := range agents {
		if agent != removedDefaultAgent {
			return fmt.Errorf(i18n.Text("config.agents_setting_removed"), agent, configPath)
		}
	}
	return nil
}
