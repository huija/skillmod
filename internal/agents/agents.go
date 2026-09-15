// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

// Package agents names the AI-agent directories skillmod can link installed
// skills into. A share destination is an unmanaged convenience link to the
// managed copy: skillmod records it nowhere, verify never looks at it, and
// sync never repairs it. The registry only maps an agent name to the skills
// directory that agent reads, so adding an agent is one entry here and
// nothing else changes.
package agents

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/huija/skillmod/internal/i18n"
)

// Target is one registered agent destination.
type Target struct {
	// Name is the spelling accepted by --agent and the interactive list.
	Name string
	// SkillsDir is the agent's skill directory relative to a scope root,
	// slash-separated. A skill appears under it with its existing directory
	// name as a symlink to the managed copy, so the managed content is the
	// one copy every agent sees.
	SkillsDir string
}

// registry maps a short agent name to the directory that agent reads skills
// from. New agents join here as their directory conventions become known.
var registry = map[string]Target{
	"claude": {Name: "claude", SkillsDir: ".claude/skills"},
	"codex":  {Name: "codex", SkillsDir: ".codex/skills"},
}

// All returns every registered target in stable name order.
func All() []Target {
	names := Names()
	out := make([]Target, 0, len(names))
	for _, name := range names {
		out = append(out, registry[name])
	}
	return out
}

// Names returns the registered names in stable sorted order.
func Names() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Lookup resolves one registered target, or names the supported ones.
func Lookup(name string) (Target, error) {
	target, ok := registry[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return Target{}, fmt.Errorf(i18n.Text("agents.unknown_target"), name, strings.Join(Names(), ", "))
	}
	return target, nil
}

// Dir returns the target's skill directory under a scope root.
func (t Target) Dir(root string) string {
	return filepath.Join(root, filepath.FromSlash(t.SkillsDir))
}
