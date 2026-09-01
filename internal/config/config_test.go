// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/huija/skillmod/internal/testutil"
)

func TestMain(m *testing.M) { testutil.RunMain(m) }

func TestDefaultAgentTarget(t *testing.T) {
	cfg := Default()
	if len(cfg.Agents) != 1 || cfg.Agents[0] != "agents" {
		t.Fatalf("Default Agents = %v, want [agents]", cfg.Agents)
	}
}

func TestPathAndLoad(t *testing.T) {
	wantPath := isolatedConfigPath(t)
	gotPath, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != wantPath {
		t.Fatalf("Path = %q, want %q", gotPath, wantPath)
	}

	// A missing file uses the defaults.
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg, Default()) {
		t.Fatalf("missing config = %+v, want %+v", cfg, Default())
	}

	if err := os.MkdirAll(filepath.Dir(wantPath), 0o755); err != nil {
		t.Fatal(err)
	}
	data := []byte("agents = [\"agents\", \"claude-code\"]\nknown_sources = [\"https://example.com/acme/skills\"]\n")
	if err := os.WriteFile(wantPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Agents, []string{"agents", "claude-code"}) ||
		!reflect.DeepEqual(cfg.KnownSources, []string{"https://example.com/acme/skills"}) {
		t.Fatalf("loaded config = %+v", cfg)
	}
}

func TestLoadEmptyAgentsFallsBackToDefault(t *testing.T) {
	p := isolatedConfigPath(t)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("known_sources = [\"file:///repo\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Agents, []string{"agents"}) || !reflect.DeepEqual(cfg.KnownSources, []string{"file:///repo"}) {
		t.Fatalf("config = %+v", cfg)
	}
}

func TestLoadErrors(t *testing.T) {
	t.Run("invalid TOML", func(t *testing.T) {
		p := isolatedConfigPath(t)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("agents = ["), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "config.toml") {
			t.Fatalf("Load error = %v, want parse error with path", err)
		}
	})

	t.Run("unreadable path", func(t *testing.T) {
		p := isolatedConfigPath(t)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(); err == nil {
			t.Fatal("Load succeeded when config.toml is a directory")
		}
	})
}

func isolatedConfigPath(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	// os.UserConfigDir uses different variables across platforms. Set every
	// relevant root so the test never reads the developer's real config.
	t.Setenv("XDG_CONFIG_HOME", base)
	t.Setenv("HOME", base)
	t.Setenv("AppData", base)
	p, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	return p
}
