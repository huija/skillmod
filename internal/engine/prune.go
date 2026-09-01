// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"fmt"
	"os"

	"github.com/huija/skillmod/internal/dirhash"
	"github.com/huija/skillmod/internal/i18n"
	"github.com/huija/skillmod/internal/modfile"
)

// Prune implements skillmod prune by cleaning installed files for stale entries present in the lock but absent from the mod.
// It lists and confirms changes first; locally modified files are kept while only their lock records are removed (PRD §3.6).
func (e *Engine) Prune(ctx context.Context, io IO) (*Report, error) {
	m, err := e.loadMod()
	if err != nil {
		return nil, err
	}
	lock := e.loadLock()
	stale := staleEntries(m, lock)
	rep := &Report{Action: "prune"}
	if len(stale) == 0 {
		rep.Notes = append(rep.Notes, i18n.Text("no stale entries"))
		io.printf(i18n.Text("no stale entries"))
		return rep, nil
	}

	adapters, err := e.adapters()
	if err != nil {
		return nil, err
	}
	var deletable []string
	newLock := &modfile.Lock{}
	staleNames := map[string]bool{}
	for _, lk := range stale {
		staleNames[lk.Name] = true
	}
	for _, lk := range lock.Skills {
		if !staleNames[lk.Name] {
			newLock.Skills = append(newLock.Skills, lk) // Keep entries that are not stale.
		}
	}
	for _, lk := range stale {
		entry := EntryReport{Name: lk.Name, Source: lk.Source, Version: lk.Version}
		for _, a := range adapters {
			dst := adapterDir(a, e.Root, lk.Name)
			h, err := dirhash.HashDir(dst)
			if err != nil {
				continue // Nothing to clean when the target does not exist.
			}
			if h == lk.Dirhash {
				deletable = append(deletable, dst)
				entry.Targets = append(entry.Targets, dst)
			} else {
				entry.Note = i18n.Text("locally modified; kept files and removed only the lock record: ") + dst
			}
		}
		entry.Action = "prune"
		rep.Entries = append(rep.Entries, entry)
	}

	if len(deletable) > 0 {
		io.printf(i18n.Text("the following directories will be deleted:"))
		for _, d := range deletable {
			io.printf("  %s", d)
		}
		ok := io.Yes
		if !ok && io.Confirm != nil {
			ok = io.Confirm.Confirm(i18n.Format("delete the %d directories listed above?", len(deletable)))
		}
		if !ok && io.Confirm == nil && !io.Yes {
			return nil, fmt.Errorf("%s", i18n.Text("the deletion list requires confirmation: retry interactively, use --yes to skip confirmation, or use --dry-run to list only"))
		}
		if !ok {
			return nil, fmt.Errorf("%s", i18n.Text("cancelled by user; no files were deleted"))
		}
	}

	if io.DryRun {
		rep.Notes = append(rep.Notes, i18n.Text("dry-run: no files were deleted"))
		return rep, nil
	}

	for _, d := range deletable {
		if err := os.RemoveAll(d); err != nil {
			return nil, fmt.Errorf(i18n.Text("delete %s: %w"), d, err)
		}
	}
	if err := modfile.SaveLock(e.Root, newLock); err != nil {
		return nil, err
	}
	io.printf(i18n.Text("pruned %d stale entries"), len(stale))
	return rep, nil
}
