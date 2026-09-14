// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"errors"
	"io/fs"
	"os"

	"github.com/huija/skillmod/internal/fsutil"
	"github.com/huija/skillmod/internal/i18n"
	"github.com/huija/skillmod/internal/modfile"
)

// Prune implements skillmod prune by cleaning installed files for stale entries present in the lock but absent from the mod.
// It lists and confirms changes first; locally modified files are kept while only their lock records are removed.
func (e *Engine) Prune(ctx context.Context, io IO, options ...MutationOptions) (*Report, error) {
	run := mutationOptions(options)
	unlock, err := e.lockState()
	if err != nil {
		return nil, err
	}
	defer unlock()
	m, err := e.loadMod()
	if err != nil {
		return nil, err
	}
	lock, err := e.loadLock()
	if err != nil {
		return nil, err
	}
	stale := staleEntries(m, lock)
	rep := &Report{Action: CommandPrune}
	if len(stale) == 0 {
		rep.Notes = append(rep.Notes, i18n.Text("engine.prune.stale_entries"))
		return rep, io.printf(i18n.Text("engine.prune.stale_entries"))
	}

	adapters, err := e.adapters()
	if err != nil {
		return nil, err
	}
	var deletable []string
	newLock := &modfile.Lock{SchemaVersion: modfile.SchemaVersion}
	staleDirs := map[string]bool{}
	for _, lk := range stale {
		staleDirs[fsutil.FoldKey(lk.InstallDir())] = true
	}
	for _, lk := range lock.Skills {
		if !staleDirs[fsutil.FoldKey(lk.InstallDir())] {
			newLock.Skills = append(newLock.Skills, lk) // Keep entries that are not stale.
		}
	}
	for _, lk := range stale {
		entry := EntryReport{Name: lk.Name, Source: lk.Source, Version: lk.Version}
		entryPartial := false
		// The recorded installation directory survives alias removal from the
		// mod file; without it, an aliased install could never be located
		// again and would leak as an orphan directory.
		dirName := lk.InstallDir()
		for _, a := range adapters {
			dst := adapterDir(a, e.Root, dirName)
			target := inspectTarget(dst, &lk)
			switch target.Action {
			case ActionMissing:
				// A dangling installation link has no target contents to preserve.
				// Remove only that entry, never its missing destination.
				if st, statErr := os.Lstat(dst); statErr == nil && st.Mode()&fs.ModeSymlink != 0 {
					if _, targetErr := os.Stat(dst); errors.Is(targetErr, fs.ErrNotExist) {
						deletable = append(deletable, dst)
						target.Action = ActionRemove
					}
				}
			case ActionInstalled:
				deletable = append(deletable, dst)
				target.Action = ActionRemove
			case ActionDrift:
				entryPartial = true
				target.Action = ActionKeep
				entry.Note = appendNote(entry.Note, i18n.Text("engine.prune.locally_modified_kept_files")+dst)
			case ActionUnverifiable:
				entryPartial = true
				target.Action = ActionKeep
				entry.Note = appendNote(entry.Note, i18n.Format("engine.remove.could_verify_kept_installed", dst, target.Note))
			}
			entry.TargetResults = append(entry.TargetResults, target)
		}
		entry.Action = ActionPrune
		if entryPartial {
			entry.Action = ActionPartial
		}
		rep.Entries = append(rep.Entries, entry)
	}

	if len(deletable) > 0 {
		if err := io.printf(i18n.Text("engine.prune.following_directories_deleted")); err != nil {
			return rep, err
		}
		for _, d := range deletable {
			if err := io.printf("  %s", d); err != nil {
				return rep, err
			}
		}
	}
	if run.DryRun {
		// dry-run must never require confirmation: the flag promises to list
		// what would happen, so the gate below is skipped entirely.
		rep.Notes = append(rep.Notes, i18n.Text("engine.prune.dry_run_files_deleted"))
		return rep, nil
	}
	if err := confirmRemovals(io, deletable, run.DryRun); err != nil {
		return nil, err
	}

	finalize, err := applyRemovals(deletable)
	if err != nil {
		return nil, err
	}
	if err := modfile.SaveLock(e.manifestRoot(), newLock); err != nil {
		return nil, errors.Join(err, finalize(false))
	}
	if err := finalize(true); err != nil {
		return nil, err
	}
	return rep, io.printf(i18n.Text("engine.prune.pruned_stale_entries"), len(stale))
}
