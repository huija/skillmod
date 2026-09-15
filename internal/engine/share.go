// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/huija/skillmod/internal/agents"
	"github.com/huija/skillmod/internal/dirhash"
	"github.com/huija/skillmod/internal/fsutil"
	"github.com/huija/skillmod/internal/i18n"
	"github.com/huija/skillmod/internal/install"
	"github.com/huija/skillmod/internal/modfile"
	"github.com/huija/skillmod/internal/source"
	"github.com/huija/skillmod/internal/ui"
)

// ShareOptions controls one share run. Skills and Agents accept explicit
// requests; when both are empty the interactive selection lists what is
// available, mirroring the collection flow of get.
type ShareOptions struct {
	Skills     []string // selectors: installation directory names or frontmatter names
	All        bool     // share every installed skill without asking
	Agents     []string // registered agent names to link into
	Dirs       []string // extra destination directories; relative ones resolve against the scope root
	OnConflict string   // ask (default), overwrite, or skip; see the Conflict constants
}

// shareSkill is one installed skill a share run can link.
type shareSkill struct {
	dirName string // installation directory under .agents/skills; also the linked directory name
	name    string // SKILL.md frontmatter name
	version string // lock baseline version; empty for a local declaration
}

// shareTarget is one destination base directory.
type shareTarget struct {
	label string // agent name for registered targets, the given directory otherwise
	path  string // absolute destination directory
}

// Share links installed skills from the scope's managed skills directory into
// agent directories. Each destination entry is a symlink to the managed copy
// — the same representation get's auto install mode uses — so the managed
// skill's edits are visible through every link at once, and a filesystem that
// forbids symlinks falls back to a byte-preserving copy exactly like get.
// Destinations are unmanaged conveniences: nothing is recorded in SKILL.mod
// or SKILL.lock, verify does not look at them, and re-running share is how a
// replaced or drifted link gets restored.
func (e *Engine) Share(ctx context.Context, options ShareOptions, io IO, options_ ...MutationOptions) (*Report, error) {
	run := mutationOptions(options_)
	unlock, err := e.lockState()
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := ValidateConflictPolicy(options.OnConflict); err != nil {
		return nil, err
	}
	policy := options.OnConflict
	if policy == "" {
		policy = ConflictAsk
	}
	lock, err := e.loadLock()
	if err != nil {
		return nil, err
	}
	listed, err := e.shareableSkills(lock)
	if err != nil {
		return nil, err
	}
	if len(listed) == 0 {
		return nil, fmt.Errorf(i18n.Text("engine.share.no_skills_installed"), e.skillsDir())
	}
	skills, err := selectShareSkills(listed, options, io)
	if err != nil {
		return nil, err
	}
	targets, err := e.selectShareTargets(options, io)
	if err != nil {
		return nil, err
	}
	if err := io.printf(i18n.Format("engine.share.plan", len(skills), len(targets))); err != nil {
		return nil, err
	}

	// Phase 1 classifies every destination before anything is written, then
	// resolves conflicts through the one resolver get, sync, and update use.
	rep := &Report{Action: CommandShare}
	installs := make([][]string, len(skills))
	var conflicts []conflict
	for i, sk := range skills {
		entry := EntryReport{Name: sk.name, Version: sk.version}
		for _, target := range targets {
			dst := filepath.Join(target.path, sk.dirName)
			action, note, err := e.classifyShareTarget(sk, dst)
			if err != nil {
				return nil, err
			}
			entry.TargetResults = append(entry.TargetResults, TargetReport{Path: dst, Action: action, Note: note})
			switch action {
			case ActionInstall:
				installs[i] = append(installs[i], dst)
			case ActionConflict:
				conflicts = append(conflicts, conflict{name: sk.name, dir: dst})
			}
		}
		rep.Entries = append(rep.Entries, entry)
	}
	skip, err := resolveConflicts(io, conflicts, policy)
	if err != nil {
		return nil, err
	}
	for i := range rep.Entries {
		for j := range rep.Entries[i].TargetResults {
			result := &rep.Entries[i].TargetResults[j]
			if result.Action != ActionConflict {
				continue
			}
			if skip[result.Path] {
				result.Action = ActionSkip
			} else {
				result.Action = ActionInstall // Overwrite was selected.
				installs[i] = append(installs[i], result.Path)
			}
		}
	}

	// Phase 2 applies the installs; conflicts are settled either way.
	skipped := 0
	for i, sk := range skills {
		for _, dst := range installs[i] {
			if run.DryRun {
				setShareTargetResult(&rep.Entries[i], dst, ActionInstall, i18n.Text("engine.share.planned_note"))
				continue
			}
			if err := e.linkShare(sk, dst); err != nil {
				return nil, err
			}
			setShareTargetResult(&rep.Entries[i], dst, ActionInstall, "")
			if err := io.printf(i18n.Format("engine.share.linked", sk.dirName, dst)); err != nil {
				return nil, err
			}
		}
		installed, kept, skippedTargets := 0, 0, 0
		for _, result := range rep.Entries[i].TargetResults {
			switch result.Action {
			case ActionInstall:
				installed++
			case ActionKeep:
				kept++
			case ActionSkip:
				skippedTargets++
				skipped++
			}
		}
		switch {
		case installed > 0 && skippedTargets > 0:
			rep.Entries[i].Action = ActionPartial
		case installed > 0:
			rep.Entries[i].Action = ActionInstall
		case skippedTargets > 0:
			rep.Entries[i].Action = ActionConflict
		case kept > 0:
			rep.Entries[i].Action = ActionKeep
		}
	}
	if run.DryRun {
		rep.Notes = append(rep.Notes, i18n.Text("engine.share.dry_run_note"))
	}
	return rep, partialError(rep, conflicts, skip)
}

// classifyShareTarget inspects one destination without writing anything:
// install when it is absent, keep when it already links to (or matches) the
// managed copy, and conflict for anything else that would be discarded.
func (e *Engine) classifyShareTarget(sk shareSkill, dst string) (TargetStatus, string, error) {
	src := e.skillDir(sk.dirName)
	st, err := os.Lstat(dst)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// A missing destination is not a conflict; nothing to preserve.
			return ActionInstall, "", nil
		}
		return "", "", err
	}
	if st.Mode()&fs.ModeSymlink != 0 {
		if resolved, linkErr := filepath.EvalSymlinks(dst); linkErr == nil {
			if managed, srcErr := filepath.EvalSymlinks(src); srcErr == nil &&
				fsutil.FoldKey(resolved) == fsutil.FoldKey(managed) {
				return ActionKeep, i18n.Format("engine.share.linked_note", dst), nil
			}
		}
		return ActionConflict, "", nil // A link to somewhere else would be discarded.
	}
	identical, err := shareIdentical(src, dst)
	if err != nil {
		// An unhashable destination (stray file, permission trouble) holds
		// content skillmod cannot compare, so it must not be replaced silently.
		return ActionConflict, "", nil
	}
	if identical {
		return ActionKeep, i18n.Format("engine.share.identical_note", dst), nil
	}
	return ActionConflict, "", nil
}

// linkShare places one destination entry through the same installation path
// get uses: a staged symlink to the managed copy (or a byte-preserving copy
// where the platform forbids links), with the previous destination preserved
// until the new one is in place.
func (e *Engine) linkShare(sk shareSkill, dst string) error {
	if err := rejectManagedOverlap(e.skillsDir(), filepath.Dir(dst)); err != nil {
		return err
	}
	_, commit, err := install.Link(e.skillDir(sk.dirName), dst)
	if err != nil {
		return err
	}
	commit()
	return nil
}

// shareIdentical reports whether the destination already holds the same
// content as the installed skill.
func shareIdentical(src, dst string) (bool, error) {
	srcHash, err := dirhash.HashDir(src)
	if err != nil {
		return false, err
	}
	dstHash, err := dirhash.HashDir(dst)
	if err != nil {
		return false, err
	}
	return srcHash == dstHash, nil
}

// setShareTargetResult rewrites one target's action and note after conflict
// resolution or installation, matching how get edits its own results.
func setShareTargetResult(entry *EntryReport, path string, action TargetStatus, note string) {
	for i := range entry.TargetResults {
		if entry.TargetResults[i].Path == path {
			entry.TargetResults[i].Action = action
			entry.TargetResults[i].Note = note
			return
		}
	}
}

// shareableSkills lists the skills a share run can link: first-level
// directories under the scope's managed skills directory that contain a
// SKILL.md. The directory name, not the frontmatter name, is what appears at
// the destination.
func (e *Engine) shareableSkills(lock *modfile.Lock) ([]shareSkill, error) {
	base := e.skillsDir()
	dents, err := os.ReadDir(base)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]shareSkill, 0, len(dents))
	for _, d := range dents {
		dir := filepath.Join(base, d.Name())
		if st, statErr := os.Stat(dir); statErr != nil || !st.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
			continue
		}
		name, err := source.SkillNameFromDir(dir)
		if err != nil {
			// Use the directory name so an invalid SKILL.md can still be
			// shared as-is, mirroring init's placeholder rule.
			name = d.Name()
		}
		sk := shareSkill{dirName: d.Name(), name: name}
		if lk := findLockByDir(lock, d.Name()); lk != nil {
			sk.version = lk.Version
		}
		out = append(out, sk)
	}
	return out, nil
}

// selectShareSkills resolves the requested skills, falling back to the
// interactive selection when nothing was requested.
func selectShareSkills(listed []shareSkill, options ShareOptions, io IO) ([]shareSkill, error) {
	switch {
	case options.All:
		return listed, nil
	case len(options.Skills) > 0:
		return matchShareSkills(listed, options.Skills)
	}
	if len(listed) == 1 || io.Yes {
		return listed, nil
	}
	optionsList := make([]ui.Option, len(listed))
	for i, sk := range listed {
		optionsList[i] = sk.option()
	}
	selected, err := chooseIndices(io,
		i18n.Text("engine.share.select_skills"),
		optionsList,
		func(index int) string { return i18n.Format("engine.share.confirm_one", listed[index].dirName) },
		i18n.Text("engine.share.no_skills_selected"))
	if err != nil {
		return nil, err
	}
	out := make([]shareSkill, 0, len(selected))
	for _, index := range selected {
		out = append(out, listed[index])
	}
	return out, nil
}

// chooseIndices resolves one multi-selection through the interaction share uses
// for both skills and destinations: a multi-select prompt where the terminal
// supports it, and one confirmation per item otherwise. confirmPrompt builds
// the per-item question for the fallback path, and emptyMessage is reported
// when nothing is selected. A missing confirmation channel is refused rather
// than read as "select nothing", so a non-interactive run fails loudly instead
// of silently sharing nothing.
func chooseIndices(io IO, prompt string, options []ui.Option, confirmPrompt func(int) string, emptyMessage string) ([]int, error) {
	if selector, ok := io.Confirm.(ui.MultiSelector); ok {
		selected, err := selector.ChooseMany(prompt, options)
		if err != nil {
			return nil, err
		}
		// A selector returning an out-of-range index cannot be trusted; treat it
		// like an empty selection rather than indexing past the list.
		for _, index := range selected {
			if index < 0 || index >= len(options) {
				return nil, fmt.Errorf("%s", emptyMessage)
			}
		}
		if len(selected) == 0 {
			return nil, fmt.Errorf("%s", emptyMessage)
		}
		return selected, nil
	}
	if io.Confirm == nil {
		return nil, fmt.Errorf("%s", i18n.Text("engine.share.non_interactive_selection"))
	}
	var selected []int
	for index := range options {
		confirmed, err := io.Confirm.Confirm(confirmPrompt(index))
		if err != nil {
			return nil, err
		}
		if confirmed {
			selected = append(selected, index)
		}
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("%s", emptyMessage)
	}
	return selected, nil
}

// matchShareSkills resolves explicit selectors against dir names and
// frontmatter names, case-insensitively like the other selection commands.
func matchShareSkills(listed []shareSkill, requested []string) ([]shareSkill, error) {
	selected := make([]shareSkill, 0, len(requested))
	seen := map[string]bool{}
	for _, token := range requested {
		matches := make([]shareSkill, 0, 2)
		for _, sk := range listed {
			if fsutil.FoldKey(sk.dirName) == fsutil.FoldKey(token) || fsutil.FoldKey(sk.name) == fsutil.FoldKey(token) {
				matches = append(matches, sk)
			}
		}
		switch len(matches) {
		case 0:
			names := make([]string, len(listed))
			for i, sk := range listed {
				names[i] = sk.dirName
			}
			return nil, fmt.Errorf(i18n.Text("engine.share.skill_not_found"), token, strings.Join(names, ", "))
		case 1:
		default:
			dirNames := make([]string, len(matches))
			for i, sk := range matches {
				dirNames[i] = sk.dirName
			}
			return nil, fmt.Errorf(i18n.Text("engine.share.skill_matches_multiple"), token, strings.Join(dirNames, ", "))
		}
		if !seen[fsutil.FoldKey(matches[0].dirName)] {
			seen[fsutil.FoldKey(matches[0].dirName)] = true
			selected = append(selected, matches[0])
		}
	}
	return selected, nil
}

// selectShareTargets resolves the destination base directories, rejecting
// anything that overlaps the managed skills directory.
func (e *Engine) selectShareTargets(options ShareOptions, io IO) ([]shareTarget, error) {
	var targets []shareTarget
	seen := map[string]bool{}
	add := func(label, path string) error {
		path, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		if err := rejectManagedOverlap(e.skillsDir(), path); err != nil {
			return err
		}
		if seen[fsutil.FoldKey(path)] {
			return nil
		}
		seen[fsutil.FoldKey(path)] = true
		targets = append(targets, shareTarget{label: label, path: path})
		return nil
	}
	for _, name := range options.Agents {
		t, err := agents.Lookup(name)
		if err != nil {
			return nil, err
		}
		if err := add(t.Name, t.Dir(e.Root)); err != nil {
			return nil, err
		}
	}
	for _, dir := range options.Dirs {
		path := dir
		if !filepath.IsAbs(path) {
			// Relative --dir values live under the scope root, next to the
			// registered agent directories rather than next to the shell.
			path = filepath.Join(e.Root, path)
		}
		if err := add(dir, path); err != nil {
			return nil, err
		}
	}
	if len(targets) > 0 {
		return targets, nil
	}
	if io.Yes {
		// --yes answers "share to the destinations I named"; it does not
		// invent destinations.
		return nil, fmt.Errorf("%s", i18n.Text("engine.share.no_targets"))
	}
	registered := agents.All()
	optionsList := make([]ui.Option, len(registered))
	for i, t := range registered {
		optionsList[i] = ui.Option{Label: t.Name, Description: t.Dir(e.Root)}
	}
	selected, err := chooseIndices(io,
		i18n.Text("engine.share.select_targets"),
		optionsList,
		func(index int) string {
			return i18n.Format("engine.share.confirm_target", registered[index].Name, registered[index].Dir(e.Root))
		},
		i18n.Text("engine.share.no_targets"))
	if err != nil {
		return nil, err
	}
	for _, index := range selected {
		t := registered[index]
		if err := add(t.Name, t.Dir(e.Root)); err != nil {
			return nil, err
		}
	}
	return targets, nil
}

// rejectManagedOverlap refuses a destination that contains, is contained in,
// or equals the managed skills directory, so a share can never shadow or
// swallow the directory skillmod verifies.
func rejectManagedOverlap(skillsDir, dir string) error {
	managed, err := resolveSharePath(skillsDir)
	if err != nil {
		return err
	}
	destination, err := resolveSharePath(dir)
	if err != nil {
		return err
	}
	for _, pair := range [][2]string{{managed, destination}, {destination, managed}} {
		rel, err := filepath.Rel(pair[0], pair[1])
		if err != nil {
			continue
		}
		if rel == "." || filepath.IsLocal(rel) {
			return fmt.Errorf(i18n.Text("engine.share.overlap_managed"), dir, skillsDir)
		}
	}
	return nil
}

// resolveSharePath resolves existing ancestors too, so a destination that does
// not exist yet cannot hide an overlap behind a parent directory symlink.
func resolveSharePath(path string) (string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	parent := filepath.Dir(path)
	if parent == path {
		return "", err
	}
	resolved, err = resolveSharePath(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, filepath.Base(path)), nil
}

func (sk shareSkill) option() ui.Option {
	description := sk.version
	if description == "" {
		description = i18n.Text("engine.share.local_description")
	}
	return ui.Option{Label: sk.dirName, Description: description}
}
