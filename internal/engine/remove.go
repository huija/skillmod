// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/huija/skillmod/internal/fsutil"
	"github.com/huija/skillmod/internal/i18n"
	"github.com/huija/skillmod/internal/modfile"
)

// Remove deletes declarations and clean managed installations selected by
// published name or installation alias. Locally modified installations are
// kept and reported as partial completion.
func (e *Engine) Remove(_ context.Context, names []string, io IO, options ...MutationOptions) (*Report, error) {
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
	adapters, err := e.adapters()
	if err != nil {
		return nil, err
	}

	want := make(map[string]bool, len(names))
	for _, name := range names {
		want[name] = true
	}
	selected := make(map[string]bool)
	found := make(map[string]bool, len(names))
	for _, skill := range m.Skills {
		if !want[skill.Name] && !want[skill.DirName()] {
			continue
		}
		selected[fsutil.FoldKey(skill.DirName())] = true
		if want[skill.Name] {
			found[skill.Name] = true
		}
		if want[skill.DirName()] {
			found[skill.DirName()] = true
		}
	}
	for _, name := range names {
		if !found[name] {
			return nil, fmt.Errorf(i18n.Text("engine.remove.entry_skill_mod"), name)
		}
	}

	newMod := &modfile.Mod{SchemaVersion: m.SchemaVersion}
	for _, skill := range m.Skills {
		if !selected[fsutil.FoldKey(skill.DirName())] {
			newMod.Skills = append(newMod.Skills, skill)
		}
	}
	newLock := &modfile.Lock{SchemaVersion: modfile.SchemaVersion}
	for _, locked := range lock.Skills {
		if !selected[fsutil.FoldKey(locked.InstallDir())] {
			newLock.Skills = append(newLock.Skills, locked)
		}
	}

	rep := &Report{Action: CommandRemove}
	var deletable []string
	partial := false
	for _, skill := range m.Skills {
		if !selected[fsutil.FoldKey(skill.DirName())] {
			continue
		}
		entry := EntryReport{Name: skill.Name, Source: skill.Source, Version: skill.Version, Action: ActionRemove}
		locked := findLock(lock, skill)
		entryPartial := false
		for _, adapter := range adapters {
			dst := adapterDir(adapter, e.Root, skill.DirName())
			target := inspectTarget(dst, locked)
			switch target.Action {
			case ActionMissing:
			case ActionUnverifiable:
				partial = true
				entryPartial = true
				target.Action = ActionKeep
				entry.Note = appendNote(entry.Note, i18n.Format("engine.remove.could_verify_kept_installed", dst, target.Note))
			case ActionUnlocked, ActionDrift:
				partial = true
				entryPartial = true
				target.Action = ActionKeep
				entry.Note = appendNote(entry.Note, i18n.Text("engine.remove.locally_modified_kept")+dst)
			case ActionInstalled:
				deletable = append(deletable, dst)
				target.Action = ActionRemove
			}
			entry.TargetResults = append(entry.TargetResults, target)
		}
		if entryPartial {
			entry.Action = ActionPartial
		}
		rep.Entries = append(rep.Entries, entry)
	}

	if len(deletable) > 0 {
		if err := io.printf(i18n.Text("engine.prune.following_directories_deleted")); err != nil {
			return rep, err
		}
		for _, dir := range deletable {
			if err := io.printf("  %s", dir); err != nil {
				return rep, err
			}
		}
	}
	if run.DryRun {
		rep.Notes = append(rep.Notes, i18n.Text("engine.prune.dry_run_files_deleted"))
		if partial {
			return rep, &PartialError{Report: rep}
		}
		return rep, nil
	}
	if err := confirmRemovals(io, deletable, run.DryRun); err != nil {
		return nil, err
	}
	finalize, err := applyRemovals(deletable)
	if err != nil {
		return nil, err
	}
	if err := e.saveState(newMod, newLock); err != nil {
		return nil, errors.Join(err, finalize(false))
	}
	if err := finalize(true); err != nil {
		return nil, err
	}
	writeErr := io.printf(i18n.Format("engine.remove.removed_declarations_clean", len(rep.Entries), len(deletable)))
	if partial {
		return rep, errors.Join(writeErr, &PartialError{Report: rep})
	}
	return rep, writeErr
}
