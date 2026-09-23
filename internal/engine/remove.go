// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/huija/skillmod/internal/address"
	"github.com/huija/skillmod/internal/fsutil"
	"github.com/huija/skillmod/internal/i18n"
	"github.com/huija/skillmod/internal/modfile"
	repoaddr "github.com/huija/skillmod/internal/repo"
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
// published name or installation alias, by repository, by --all, or from the
// interactive selection when the run names nothing. --all states the whole
// declared set, or the whole set that the repository selectors name, which is
// what makes "everything this repository brought in" sayable without a
// terminal. Locally modified installations are kept and reported as partial
// completion.
func (e *Engine) Remove(ctx context.Context, args []string, io IO, options ...RemoveOptions) (*Report, error) {
	run := removeOptions(options)
	if run.All {
		// --all states the whole set, and a repository selector is a set it can
		// expand; a skill name already names the entry, so stating both is
		// refused rather than guessed at.
		for _, arg := range args {
			if !strings.Contains(arg, "/") {
				return nil, fmt.Errorf("%s", i18n.Text("engine.remove.all_exclusive"))
			}
		}
	}
	if len(run.Agents) > 0 {
		// Unlinking names agents on an entry, so a repository selector resolves
		// to the names of the entries it matches before the delegation.
		m, err := e.loadMod()
		if err != nil {
			return nil, err
		}
		names, err := repositoryRemovalNames(m, args, io, run.All)
		if err != nil {
			return nil, err
		}
		// A repository selector expands to the names of every entry it declares,
		// which states the set by itself; forwarding --all alongside those names
		// would state it a second time, and share refuses a set stated twice.
		// --all on its own still travels as --all. The refusal is share's, not
		// this one, so a run that names skills next to --all is still rejected
		// where the two collide, above.
		all := run.All && len(names) == 0
		rep, err := e.Share(ctx, ShareOptions{Skills: names, All: all, Remove: run.Agents}, io,
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
	selected, err := selectRemovals(m, args, run.All, io)
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
// installation directory. Names and --all state the set explicitly, and --all
// also states the whole set a repository selector matches; a repository
// selector without it picks the entries declared from that repository and asks
// which of them when more than one is declared; with neither, an interactive
// caller picks from what the manifest declares, which is how every other
// selection in skillmod works. Removing deletes an installation outright, so
// the picker is only how the set is chosen — the confirmation that lists the
// directories still comes afterwards.
func selectRemovals(m *modfile.Mod, args []string, all bool, io IO) (map[string]bool, error) {
	if all && len(args) == 0 {
		if len(m.Skills) == 0 {
			return nil, fmt.Errorf("%s", i18n.Text("engine.remove.nothing_declared"))
		}
		selected := make(map[string]bool, len(m.Skills))
		for _, skill := range m.Skills {
			selected[fsutil.FoldKey(skill.DirName())] = true
		}
		return selected, nil
	}
	selected := map[string]bool{}
	var names []string
	for _, arg := range args {
		// A skill name or alias can never contain a slash, so an argument that
		// does is a repository selector.
		if !strings.Contains(arg, "/") {
			names = append(names, arg)
			continue
		}
		fromRepo, err := repositoryRemovals(m, arg, io, all)
		if err != nil {
			return nil, err
		}
		for _, skill := range fromRepo {
			selected[fsutil.FoldKey(skill.DirName())] = true
		}
	}
	if len(names) > 0 {
		named, err := namedRemovals(m, names)
		if err != nil {
			return nil, err
		}
		for key := range named {
			selected[key] = true
		}
	}
	if len(selected) > 0 {
		return selected, nil
	}
	if len(m.Skills) == 0 {
		return nil, fmt.Errorf("%s", i18n.Text("engine.remove.nothing_declared"))
	}
	// --yes answers the confirmation; it does not choose what to delete. A run
	// without a channel to ask through is refused, with
	// the message that names the two ways to say it explicitly, rather than
	// the generic empty-selection one.
	if io.Confirm == nil {
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
	for _, index := range picked {
		selected[fsutil.FoldKey(m.Skills[index].DirName())] = true
	}
	return selected, nil
}

// repositoryRemovals resolves one repository selector against the
// declarations. A repository that declares a single skill needs no choice, and
// all takes every entry it declares; several entries without either are
// offered interactively, or listed for a rerun when there is no channel to ask
// through.
func repositoryRemovals(m *modfile.Mod, raw string, io IO, all bool) ([]modfile.ModSkill, error) {
	addr, err := address.Parse(raw)
	if err != nil {
		return nil, err
	}
	matched := entriesFromRepository(m, addr.Repo)
	if len(matched) == 0 {
		return nil, fmt.Errorf(i18n.Text("engine.remove.no_entry_from_repository"), raw)
	}
	if len(matched) == 1 || all {
		return matched, nil
	}
	if io.Confirm == nil {
		displayOptions := make([]string, len(matched))
		for i, skill := range matched {
			displayOptions[i] = removalOption(skill).Label
		}
		return nil, &repositoryCandidatesError{Repo: raw, Candidates: displayOptions}
	}
	options := make([]ui.Option, len(matched))
	for i, skill := range matched {
		options[i] = removalOption(skill)
	}
	picked, err := chooseIndices(io,
		i18n.Format("engine.remove.select_from_repository", raw),
		options,
		func(index int) string { return i18n.Format("engine.remove.confirm_one", matched[index].DirName()) },
		i18n.Text("engine.remove.no_entries_selected"))
	if err != nil {
		return nil, err
	}
	out := make([]modfile.ModSkill, 0, len(picked))
	for _, index := range picked {
		out = append(out, matched[index])
	}
	return out, nil
}

// repositoryRemovalNames resolves every repository selector in args and
// returns the names of the entries they match, with the remaining arguments
// passed through unchanged. It is what the --agent path uses, because
// unlinking addresses entries by name.
func repositoryRemovalNames(m *modfile.Mod, args []string, io IO, all bool) ([]string, error) {
	names := make([]string, 0, len(args))
	for _, arg := range args {
		if !strings.Contains(arg, "/") {
			names = append(names, arg)
			continue
		}
		fromRepo, err := repositoryRemovals(m, arg, io, all)
		if err != nil {
			return nil, err
		}
		for _, skill := range fromRepo {
			names = append(names, skill.Name)
		}
	}
	return names, nil
}

// entriesFromRepository returns the declarations installed from one
// repository, in declaration order. Local entries never match.
func entriesFromRepository(m *modfile.Mod, repo string) []modfile.ModSkill {
	var matched []modfile.ModSkill
	for _, skill := range m.Skills {
		if skill.Source == "" {
			continue
		}
		entryRepo, _, err := splitSource(skill.Source)
		if err != nil {
			continue // A malformed declaration is reported by the commands that resolve it.
		}
		if repoaddr.Identity(entryRepo) == repoaddr.Identity(repo) {
			matched = append(matched, skill)
		}
	}
	return matched
}

type repositoryCandidatesError struct {
	Repo       string
	Candidates []string
}

func (e *repositoryCandidatesError) Error() string {
	return i18n.Format("engine.remove.multiple_from_repository", e.Repo, strings.Join(e.Candidates, ", "))
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
