// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

// Package agents names the AI-agent directories skillmod can link installed
// skills into. An agent is named by one directory segment and reads its skills
// from ".<name>/skills" under a scope root, which is the convention they share,
// so the name alone locates the directory: share links a skill there, records
// the name on the skill's [[skill]] entry in SKILL.mod, sync recreates the link
// from that record on a new machine, verify reports a missing or drifted link
// under an agent directory that exists here, and remove and prune take the
// links down with the managed copies they point at. Recording the name rather
// than the resolved path is what keeps the declaration portable, and the rule
// is why any single-segment name works: the list below only decides what an
// interactive selection suggests, never what the command accepts.
package agents

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/huija/skillmod/internal/fsutil"
	"github.com/huija/skillmod/internal/i18n"
)

// Target is one agent destination.
type Target struct {
	// Name is the spelling accepted by --agent and shown in the interactive
	// list: one directory segment, lowercase, without its leading dot.
	Name string
}

// known lists the agents an interactive selection suggests. Naming one is
// never required, so this list is a convenience for the common ones rather
// than a registry the command is bound to.
var known = []string{"claude", "codex"}

// Known returns the suggested agent names in stable order.
func Known() []string {
	return append([]string(nil), known...)
}

// Resolve maps an agent name to its destination. Every agent follows the
// ".<name>/skills" rule, so a name is checked for the shape of one portable
// directory segment rather than against a fixed list, and an agent that ships
// after this release needs no change here. A leading dot is accepted and
// dropped, because the directory is what the caller sees in the tree, and the
// name is lowercased so one agent keeps one spelling wherever it is typed.
func Resolve(name string) (Target, error) {
	segment := strings.ToLower(strings.TrimLeft(strings.TrimSpace(name), "."))
	if err := fsutil.ValidAlias(segment); err != nil {
		return Target{}, fmt.Errorf(i18n.Text("agents.invalid_name"), name, err)
	}
	if err := fsutil.ValidName("." + segment); err != nil {
		return Target{}, fmt.Errorf(i18n.Text("agents.invalid_name"), name, err)
	}
	return Target{Name: segment}, nil
}

// Dir returns the target's skill directory under a scope root.
func (t Target) Dir(root string) string {
	return filepath.Join(root, "."+t.Name, "skills")
}

// Suggested returns the destinations an interactive selection offers, in
// stable name order: the known agents plus every ".<name>/skills" directory
// that already exists under the root, so an agent named for the first time on
// the command line comes back as a choice on the next run. The managed skills
// directory follows the same shape, and the caller filters it out because it
// is the one destination skillmod owns.
func Suggested(root string) []Target {
	names := Known()
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		seen[name] = true
	}
	for _, name := range present(root) {
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	sort.Strings(names)
	targets := make([]Target, len(names))
	for i, name := range names {
		targets[i] = Target{Name: name}
	}
	return targets
}

// present lists the agent names carried by the ".<name>/skills" directories
// under the root, lowercased like Resolve lowercases a typed name.
func present(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var names []string
	for _, entry := range entries {
		dotName := entry.Name()
		if !strings.HasPrefix(dotName, ".") {
			continue
		}
		target, resolveErr := Resolve(dotName)
		if resolveErr != nil {
			continue
		}
		st, statErr := os.Stat(filepath.Join(root, dotName, "skills"))
		if statErr != nil || !st.IsDir() {
			continue
		}
		canonical, canonicalErr := os.Stat(target.Dir(root))
		if canonicalErr == nil && os.SameFile(st, canonical) {
			// Do not offer a spelling whose normalized name locates a
			// different directory on this filesystem.
			names = append(names, target.Name)
		}
	}
	return names
}
