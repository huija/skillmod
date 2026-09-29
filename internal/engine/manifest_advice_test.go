// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package engine_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/huija/skillmod/internal/engine"
)

// The missing-manifest advice is a recovery contract: it must name the paths
// that actually recover the user. A project without a manifest is usually a
// user standing in $HOME — whose installation directories are the global
// scope's — so the project advice offers --global before suggesting init;
// the global advice spells the same flag the command already needs. Rewording
// either message also updates TestContractWordingIsPinned in the same change.
func TestMissingManifestAdviceNamesScopeRecoveryPaths(t *testing.T) {
	project := newEngine(t, t.TempDir(), t.TempDir())
	_, err := project.Sync(ctx, engine.SyncOptions{}, testIO())
	if err == nil {
		t.Fatal("Sync(project without manifest) = nil, want missing-manifest error")
	}
	for _, want := range []string{"SKILL.mod not found", "skillmod init", "--global"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Sync(project without manifest) = %q, want advice containing %q", err, want)
		}
	}

	global := newEngine(t, t.TempDir(), t.TempDir())
	global.ManifestRoot = filepath.Join(global.Store.Root(), "global")
	_, err = global.Sync(ctx, engine.SyncOptions{}, testIO())
	if err == nil {
		t.Fatal("Sync(global without manifest) = nil, want missing-manifest error")
	}
	if !strings.Contains(err.Error(), "--global init") {
		t.Errorf("Sync(global without manifest) = %q, want advice containing --global init", err)
	}
	if strings.Contains(err.Error(), "project") {
		t.Errorf("Sync(global without manifest) = %q, want no project-scoped advice", err)
	}
}
