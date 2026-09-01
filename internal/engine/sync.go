// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"path/filepath"

	"github.com/huija/skillmod/internal/dirhash"
	"github.com/huija/skillmod/internal/i18n"
	"github.com/huija/skillmod/internal/install"
)

// adapterDir returns an entry's installation directory for a platform.
func adapterDir(a install.Adapter, root, dir string) string {
	return filepath.Join(a.SkillsDir(root), dir)
}

// Sync reconciles local skill directories with SKILL.lock (PRD §3.3):
// it is idempotent and verifiable, rolls back on failure, and never deletes installed files automatically.
func (e *Engine) Sync(ctx context.Context, checkOnly bool, io IO) (*Report, error) {
	if checkOnly {
		return e.Verify(ctx, io) // sync --check is an alias for verify and uses the same implementation.
	}
	m, err := e.loadMod()
	if err != nil {
		return nil, err
	}
	lock := e.loadLock()

	entries, newLock, err := e.align(ctx, m, lock)
	if err != nil {
		return nil, err
	}
	adapters, err := e.adapters()
	if err != nil {
		return nil, err
	}

	rep := &Report{Action: "sync"}
	var plans []plannedInstall
	var conflicts []conflict

	for _, en := range entries {
		prevHash := ""
		if old := findLock(lock, en.skill.Name); old != nil {
			prevHash = old.Dirhash // The pre-alignment lock hash allows a clean old version to be overwritten.
		}
		var targets []string
		conflictsBefore := len(conflicts)
		for _, a := range adapters {
			dst := adapterDir(a, e.Root, en.skill.DirName())
			switch classifyTarget(dst, en.dirhash, prevHash) {
			case "install":
				targets = append(targets, dst)
			case "conflict":
				conflicts = append(conflicts, conflict{name: en.skill.DirName(), dir: dst})
			}
		}
		action := "keep"
		if len(targets) > 0 {
			action = "install"
		}
		if len(conflicts) > conflictsBefore {
			action = "conflict"
		}
		rep.Entries = append(rep.Entries, EntryReport{
			Name: en.skill.Name, Source: en.skill.Source, Version: en.version,
			Action: action, Note: en.note, Targets: targets,
		})
		if len(targets) > 0 {
			plans = append(plans, plannedInstall{name: en.skill.DirName(), contentDir: en.contentDir, targets: targets})
		}
	}

	// Validate local entries without modifying them; warn about drift but do not repair it.
	for _, sk := range m.Skills {
		if !sk.Local {
			continue
		}
		lk := findLock(lock, sk.Name)
		if lk == nil {
			rep.Entries = append(rep.Entries, EntryReport{
				Name: sk.Name, Action: "local", Note: i18n.Text("no baseline in the lock; verification skipped (run init again to establish one)")})
			continue
		}
		for _, a := range adapters {
			dst := adapterDir(a, e.Root, sk.DirName())
			h, err := dirhash.HashDir(dst)
			switch {
			case err != nil:
				rep.Entries = append(rep.Entries, EntryReport{Name: sk.Name, Action: "local", Note: i18n.Text("missing: ") + dst})
			case h != lk.Dirhash:
				rep.Entries = append(rep.Entries, EntryReport{Name: sk.Name, Action: "local-drift", Note: i18n.Text("contents do not match the baseline (local changes)"), Targets: []string{dst}})
			default:
				rep.Entries = append(rep.Entries, EntryReport{Name: sk.Name, Action: "local", Note: i18n.Text("consistent")})
			}
		}
	}

	// Leave stale entry files untouched and recommend prune (PRD §3.3 rule 7).
	for _, lk := range staleEntries(m, lock) {
		rep.Entries = append(rep.Entries, EntryReport{
			Name: lk.Name, Source: lk.Source, Version: lk.Version,
			Action: "stale", Note: i18n.Text("removed from the mod; files were kept—run skillmod prune to clean them"),
		})
	}

	// Add conflict targets selected for overwrite to the plan.
	skip, err := resolveConflicts(io, conflicts)
	if err != nil {
		return nil, err
	}
	for _, en := range entries {
		prevHash := ""
		if old := findLock(lock, en.skill.Name); old != nil {
			prevHash = old.Dirhash
		}
		for _, a := range adapters {
			dst := adapterDir(a, e.Root, en.skill.DirName())
			if classifyTarget(dst, en.dirhash, prevHash) == "conflict" && !skip[dst] {
				plans = append(plans, plannedInstall{name: en.skill.DirName(), contentDir: en.contentDir, targets: []string{dst}})
			}
		}
	}

	if io.DryRun {
		rep.Notes = append(rep.Notes, i18n.Text("dry-run: no files were written"))
		return rep, nil
	}

	finalize, err := applyInstalls(plans)
	if err != nil {
		return nil, err
	}
	if err := e.saveLockIfChanged(newLock); err != nil {
		finalize(false)
		return nil, err
	}
	if err := finalize(true); err != nil {
		return nil, err
	}

	changed := 0
	for _, en := range rep.Entries {
		if en.Action == "install" {
			changed++
		}
	}
	if changed == 0 {
		io.printf(i18n.Text("no changes")) // Idempotency required by AC-2.
	} else {
		io.printf(i18n.Text("synchronized %d entries; verification passed"), changed)
	}
	return rep, nil
}
