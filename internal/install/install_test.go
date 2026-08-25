// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package install

import (
	"path/filepath"
	"testing"
)

func TestDefaultAndExplicitAdapters(t *testing.T) {
	adapters, err := ByNames(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(adapters) != 1 || adapters[0].Name() != "agents" {
		t.Fatalf("default adapters = %v, want [agents]", Names())
	}
	if got := adapters[0].SkillsDir("/project"); got != filepath.Join("/project", ".agents", "skills") {
		t.Fatalf("agents SkillsDir = %q", got)
	}

	adapters, err = ByNames([]string{"agents", "claude-code"})
	if err != nil {
		t.Fatal(err)
	}
	if len(adapters) != 2 || adapters[1].SkillsDir("/project") != filepath.Join("/project", ".claude", "skills") {
		t.Fatalf("explicit adapters = %+v", adapters)
	}
}

func TestAllIsStable(t *testing.T) {
	all := All()
	if len(all) != 2 || all[0].Name() != "agents" || all[1].Name() != "claude-code" {
		t.Fatalf("All = %+v", all)
	}
}
