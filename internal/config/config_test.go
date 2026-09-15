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

func TestDefaultConfiguration(t *testing.T) {
	cfg := Default()
	if cfg.InstallMode != "" || len(cfg.KnownSources) != 0 {
		t.Fatalf("Default = %+v, want an empty configuration", cfg)
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
	data := []byte("install_mode = \"copy\"\nknown_sources = [\"https://example.com/acme/skills\"]\n")
	if err := os.WriteFile(wantPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.InstallMode != "copy" || !reflect.DeepEqual(cfg.KnownSources, []string{"https://example.com/acme/skills"}) {
		t.Fatalf("loaded config = %+v", cfg)
	}
}

func TestLoadKeepsKnownSources(t *testing.T) {
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
	if !reflect.DeepEqual(cfg.KnownSources, []string{"file:///repo"}) {
		t.Fatalf("config = %+v", cfg)
	}
}

// A configuration that selected an installation platform asks for a directory
// skillmod no longer manages, so it fails loudly instead of installing somewhere
// the file does not describe.
func TestLoadRejectsRemovedInstallationPlatform(t *testing.T) {
	for _, agents := range []string{
		"agents = [\"claude-code\"]\n",
		"agents = [\"agents\", \"claude-code\"]\ninstall_mode = \"copy\"\n",
	} {
		p := isolatedConfigPath(t)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(agents), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := Load()
		if err == nil {
			t.Fatalf("Load(%q) succeeded, want the removed setting rejected", agents)
		}
		for _, want := range []string{"agents", "claude-code", p} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("Load(%q) error = %q, want it to name %q", agents, err, want)
			}
		}
	}
}

// The adapter name for the directory skillmod still manages described exactly
// today's behavior, so a configuration carrying only it keeps working.
func TestLoadToleratesRemainingAgentName(t *testing.T) {
	p := isolatedConfigPath(t)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("agents = [\"agents\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err != nil {
		t.Fatalf("Load: %v", err)
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

func TestLoadInstallMode(t *testing.T) {
	p := isolatedConfigPath(t)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("install_mode = \"copy\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.InstallMode != "copy" {
		t.Errorf("Load install mode = %q, want copy", cfg.InstallMode)
	}
	if err := os.WriteFile(p, []byte("install_mode = \"unknown\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Error("Load accepted invalid install mode")
	}
}
