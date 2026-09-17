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
	"github.com/huija/skillmod/internal/ui"
)

// RemoveOptions controls one remove run.
type RemoveOptions struct {
	All    bool // remove every declared entry without asking
	DryRun bool
	Agents []string // unlink these agents while keeping declarations and managed copies
}

func removeOptions(options []RemoveOptions) RemoveOptions {
	if len(options) == 0 {
		return RemoveOptions{}
	}
	return options[0]
}

// Remove deletes declarations and clean managed installations selected by
// published name or installation alias, by --all, or from the interactive
// selection when the run names nothing. Locally modified installations are
// kept and reported as partial completion.
func (e *Engine) Remove(ctx context.Context, names []string, io IO, options ...RemoveOptions) (*Report, error) {
	run := removeOptions(options)
	if run.All && len(names) > 0 {
		return nil, fmt.Errorf("%s", i18n.Text("engine.remove.all_exclusive"))
	}
	if len(run.Agents) > 0 {
		rep, err := e.Share(ctx, ShareOptions{Skills: names, All: run.All, Remove: run.Agents}, io,
			MutationOptions{DryRun: run.DryRun})
		if rep != nil {
			rep.Action = CommandRemove
		}
		return rep, err
	}
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
	selected, err := selectRemovals(m, names, run.All, io)
	if err != nil {
		return nil, err
	}

	// Every remaining entry keeps its own agents list; a removed skill's list
	// goes with the entry, and its links are taken down below.
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
	var shareDeletable []string
	partial := false
	for _, skill := range m.Skills {
		if !selected[fsutil.FoldKey(skill.DirName())] {
			continue
		}
		entry := EntryReport{Name: skill.Name, Source: skill.Source, Version: skill.Version, Action: ActionRemove}
		locked := findLock(lock, skill)
		entryPartial := false
		{
			dst := e.skillDir(skill.DirName())
			target := inspectTarget(dst, locked)
			switch target.Action {
			case ActionMissing:
				// The managed copy is gone already, but its declared share
				// links may dangle on; they go with the declaration.
				links, linkErr := e.shareLinksToClean(lock, skill.DirName(), skill.Name)
				if linkErr != nil {
					return nil, linkErr
				}
				shareDeletable = append(shareDeletable, links...)
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
				links, linkErr := e.shareLinksToClean(lock, skill.DirName(), skill.Name)
				if linkErr != nil {
					return nil, linkErr
				}
				shareDeletable = append(shareDeletable, links...)
			}
			entry.TargetResults = append(entry.TargetResults, target)
		}
		if entryPartial {
			entry.Action = ActionPartial
		}
		rep.Entries = append(rep.Entries, entry)
	}

	if len(deletable) > 0 {
		if err := io.printf(i18n.Text("engine.remove.following_directories_deleted")); err != nil {
			return rep, err
		}
		for _, dir := range deletable {
			if err := io.printf("  %s", dir); err != nil {
				return rep, err
			}
		}
	}
	// The share links leave with the managed copies, so the confirmation (and
	// the dry-run listing, which returns below) must say so before it happens.
	if err := reportShareRemovals(io, shareDeletable, i18n.Text("engine.remove.following_share_links_deleted")); err != nil {
		return rep, err
	}
	if run.DryRun {
		rep.Notes = append(rep.Notes, i18n.Text("engine.remove.dry_run_files_deleted"))
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
	// Share links into agent directories point at the managed copies that were
	// just removed; take the links down with them. Foreign content is already
	// filtered out, and a failure here leaves only a broken link that prune can
	// still find, so it is reported rather than aborting the whole removal.
	if err := e.applyShareRemovals(shareDeletable); err != nil {
		writeErr := io.printf(i18n.Text("engine.remove.share_cleanup_failed"))
		return rep, errors.Join(writeErr, err)
	}
	writeErr := io.printf(i18n.Format("engine.remove.removed_declarations_clean", len(rep.Entries), len(deletable)))
	if partial {
		return rep, errors.Join(writeErr, &PartialError{Report: rep})
	}
	return rep, writeErr
}

// selectRemovals resolves the entries a remove run acts on, keyed by folded
// installation directory. Names and --all state the set explicitly; with
// neither, an interactive caller picks from what the manifest declares, which
// is how every other selection in skillmod works. Removing deletes an
// installation outright, so the picker is only how the set is chosen — the
// confirmation that lists the directories still comes afterwards.
func selectRemovals(m *modfile.Mod, names []string, all bool, io IO) (map[string]bool, error) {
	if all {
		if len(m.Skills) == 0 {
			return nil, fmt.Errorf("%s", i18n.Text("engine.remove.nothing_declared"))
		}
		selected := make(map[string]bool, len(m.Skills))
		for _, skill := range m.Skills {
			selected[fsutil.FoldKey(skill.DirName())] = true
		}
		return selected, nil
	}
	if len(names) > 0 {
		return namedRemovals(m, names)
	}
	if len(m.Skills) == 0 {
		return nil, fmt.Errorf("%s", i18n.Text("engine.remove.nothing_declared"))
	}
	// --yes answers the confirmation; it does not choose what to delete. A run
	// without a channel to ask through is refused for the same reason, with
	// the message that names the two ways to say it explicitly, rather than
	// the generic empty-selection one.
	if io.Yes || io.Confirm == nil {
		return nil, fmt.Errorf("%s", i18n.Text("engine.remove.needs_selection"))
	}
	options := make([]ui.Option, len(m.Skills))
	for i, skill := range m.Skills {
		options[i] = removalOption(skill)
	}
	picked, err := chooseIndices(io,
		i18n.Text("engine.remove.select_entries"),
		options,
		func(index int) string { return i18n.Format("engine.remove.confirm_one", m.Skills[index].DirName()) },
		i18n.Text("engine.remove.no_entries_selected"))
	if err != nil {
		return nil, err
	}
	selected := make(map[string]bool, len(picked))
	for _, index := range picked {
		selected[fsutil.FoldKey(m.Skills[index].DirName())] = true
	}
	return selected, nil
}

// namedRemovals resolves the requested names against the declarations. A name
// selects every entry that answers to it, whether it was published under that
// name or installed under that alias, and a name that matches nothing is
// reported rather than ignored.
func namedRemovals(m *modfile.Mod, names []string) (map[string]bool, error) {
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
	return selected, nil
}

// removalOption renders one declaration for the interactive selection. The
// directory name is what the entry is called on disk, and the version is what
// tells two entries with the same name apart.
func removalOption(skill modfile.ModSkill) ui.Option {
	description := skill.Version
	if description == "" {
		description = i18n.Text("engine.remove.local_description")
	}
	return ui.Option{Label: skill.DirName(), Description: description}
}
