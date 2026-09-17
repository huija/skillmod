// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"errors"

	"github.com/huija/skillmod/internal/dirhash"
	"github.com/huija/skillmod/internal/i18n"
	"github.com/huija/skillmod/internal/modfile"
)

// SyncOptions controls reconciliation behavior without mixing command policy
// into the input/output channels.
type SyncOptions struct {
	CheckOnly bool
	Relink    bool
	DryRun    bool
}

// Sync reconciles local skill directories with SKILL.lock:
// it is idempotent and verifiable, rolls back on failure, and never deletes installed files automatically.
func (e *Engine) Sync(ctx context.Context, options SyncOptions, io IO) (*Report, error) {
	if options.CheckOnly {
		return e.Verify(ctx, io) // sync --check is an alias for verify and uses the same implementation.
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

	entries, newLock, err := e.align(ctx, m, lock)
	if err != nil {
		return nil, err
	}
	rep := &Report{Action: CommandSync}
	var plans []plannedInstall
	var conflicts []conflict
	// Entry conflicts are remembered so resolveConflicts' overwrite selections
	// can extend the plan without reclassifying and rehashing every target.
	conflictsByEntry := make([][]string, len(entries))

	for i, en := range entries {
		prevHash := ""
		if old := findLock(lock, en.skill); old != nil {
			prevHash = old.Dirhash // The pre-alignment lock hash allows a clean old version to be overwritten.
		}
		var targets []string
		var targetResults []TargetReport
		dst := e.skillDir(en.skill.DirName())
		targetAction := classifyTarget(dst, en.dirhash, prevHash)
		if targetAction == ActionKeep && options.Relink {
			targetAction = ActionInstall
		}
		targetResults = append(targetResults, TargetReport{Path: dst, Action: targetAction})
		switch targetAction {
		case ActionInstall:
			targets = append(targets, dst)
		case ActionConflict:
			conflicts = append(conflicts, conflict{name: en.skill.DirName(), dir: dst})
			conflictsByEntry[i] = append(conflictsByEntry[i], dst)
		}
		action := EntryStatus(ActionKeep)
		if len(targets) > 0 {
			action = ActionInstall
		}
		if len(conflictsByEntry[i]) > 0 {
			action = ActionConflict
		}
		rep.Entries = append(rep.Entries, EntryReport{
			Name: en.skill.Name, Source: en.skill.Source, Version: en.version,
			Directory: en.skill.DirName(), Action: action, Note: en.note, TargetResults: targetResults,
		})
		if len(targets) > 0 {
			plans = append(plans, plannedInstall{name: en.skill.DirName(), contentDir: en.contentDir, targets: targets})
		}
	}

	// Validate local entries without modifying them; warn about drift but do not repair it.
	// A remote lock record left behind when a mod entry was hand-edited from
	// remote to local still occupies the installation-directory slot; the
	// baseline is re-established from the installed files (only the lock is
	// updated, never the files).
	for _, sk := range m.Skills {
		if !sk.Local {
			continue
		}
		lk := findLock(lock, sk)
		if lk == nil {
			if findLockByDir(lock, sk.DirName()) != nil {
				e.reestablishLocalBaseline(newLock, sk, rep)
				continue
			}
			rep.Entries = append(rep.Entries, EntryReport{
				Name: sk.Name, Directory: sk.DirName(), Action: ActionLocal, Note: i18n.Text("engine.sync.no_baseline")})
			continue
		}
		dst := e.skillDir(sk.DirName())
		h, err := dirhash.HashDir(dst)
		switch {
		case err != nil:
			rep.Entries = append(rep.Entries, EntryReport{Name: sk.Name, Directory: sk.DirName(), Action: ActionLocal, Note: i18n.Text("engine.sync.missing") + dst})
		case h != lk.Dirhash:
			rep.Entries = append(rep.Entries, EntryReport{Name: sk.Name, Directory: sk.DirName(), Action: ActionLocalDrift, Note: i18n.Text("engine.sync.contents_match_baseline_local"), TargetResults: []TargetReport{{Path: dst, Action: ActionDrift}}})
		default:
			rep.Entries = append(rep.Entries, EntryReport{Name: sk.Name, Directory: sk.DirName(), Action: ActionLocal, Note: i18n.Text("engine.sync.consistent")})
		}
	}

	// Leave stale entry files untouched and recommend prune.
	for _, lk := range staleEntries(m, lock) {
		rep.Entries = append(rep.Entries, EntryReport{
			Name: lk.Name, Source: lk.Source, Version: lk.Version,
			Action: ActionStale, Note: i18n.Text("engine.sync.removed_mod_files_kept"),
		})
	}

	// Add conflict targets selected for overwrite to the plan, and classify
	// the declared share destinations so their conflicts settle together.
	sharePlans, shareConflicts, err := e.classifyShareLinks(m, rep, entries)
	if err != nil {
		return nil, err
	}
	for _, c := range shareConflicts {
		conflicts = append(conflicts, c.asConflict())
	}
	skip, err := resolveConflicts(io, conflicts, ConflictAsk)
	if err != nil {
		return nil, err
	}
	for i := range entries {
		var overwrite []string
		skipped := 0
		for _, dst := range conflictsByEntry[i] {
			if skip[dst] {
				skipped++
				setTargetResult(&rep.Entries[i], dst, ActionSkip)
				continue
			}
			overwrite = append(overwrite, dst)
			setTargetResult(&rep.Entries[i], dst, ActionInstall)
		}
		if len(overwrite) > 0 {
			plans = append(plans, plannedInstall{name: entries[i].skill.DirName(), contentDir: entries[i].contentDir, targets: overwrite})
		}
		if skipped > 0 {
			note := i18n.Format("engine.sync.conflicting_targets_kept", skipped)
			rep.Entries[i].Note = appendNote(rep.Entries[i].Note, note)
		}
	}
	for _, c := range shareConflicts {
		entry := findEntryByDirectory(rep, c.dirName)
		if skip[c.dir] {
			setShareTargetResult(entry, c.dir, ActionSkip, "")
			continue
		}
		setShareTargetResult(entry, c.dir, ActionInstall, "")
		sharePlans = append(sharePlans, plannedShareLink{skill: shareSkill{name: c.name, dirName: c.dirName}, dst: c.dir})
	}
	// The aggregate includes both managed installations and shared links. In
	// particular, restoring only a missing share link is still an installation.
	for i := range rep.Entries {
		entry := &rep.Entries[i]
		changed, skipped := false, false
		for _, target := range entry.TargetResults {
			changed = changed || target.Action == ActionInstall
			skipped = skipped || target.Action == ActionSkip
		}
		switch {
		case changed && (skipped || entry.Action == ActionLocalDrift):
			entry.Action = ActionPartial
		case changed:
			entry.Action = ActionInstall
		case skipped:
			entry.Action = ActionConflict
		}
	}
	// Keep past destinations for cleanup, and record all currently declared
	// ones even when this machine did not have an installed copy before sync.
	for _, skill := range m.Skills {
		targets, err := e.shareTargetsFor(m, skill.DirName())
		if err != nil {
			return nil, err
		}
		names := make([]string, 0, len(targets))
		for _, target := range targets {
			names = append(names, target.label)
		}
		recordLockAgents(newLock, skill.DirName(), names)
	}

	if options.DryRun {
		// The flag promises to print the execution plan, so summarize it here
		// before returning; nothing is written in dry-run mode.
		planned := 0
		for _, en := range rep.Entries {
			if en.Action == ActionInstall || en.Action == ActionPartial {
				planned++
			}
		}
		skipped := skippedConflictCount(conflicts, skip)
		var writeErr error
		if planned == 0 && skipped > 0 {
			writeErr = io.printf(i18n.Format("engine.sync.dry_run_writes_planned", skipped))
		} else if planned == 0 {
			writeErr = io.printf(i18n.Text("engine.sync.dry_run_everything_already"))
		} else {
			writeErr = io.printf(i18n.Format("engine.sync.dry_run_entries_installed", planned))
		}
		rep.Notes = append(rep.Notes, i18n.Text("engine.dry_run_files_written"))
		return rep, errors.Join(writeErr, partialError(rep, conflicts, skip))
	}

	finalize, err := applyInstallsWithMode(plans, e.Config.InstallMode)
	if err != nil {
		return nil, err
	}
	if err := e.saveState(m, newLock); err != nil {
		return nil, errors.Join(err, finalize(false))
	}
	if err := finalize(true); err != nil {
		return nil, err
	}
	if err := e.applyShareLinks(sharePlans, rep); err != nil {
		// The managed installs and the manifest state are final; a share link
		// is a convenience sync recreates, so the failure is reported the same
		// way remove and prune report theirs — with the report intact rather
		// than discarded, and a rerun picks the missing links up.
		writeErr := io.printf(i18n.Text("engine.sync.share_relink_failed"))
		return rep, errors.Join(writeErr, err)
	}

	changed := 0
	for _, en := range rep.Entries {
		if en.Action == ActionInstall || en.Action == ActionPartial {
			changed++
		}
	}
	if changed == 0 {
		if skipped := skippedConflictCount(conflicts, skip); skipped > 0 {
			err = io.printf(i18n.Format("engine.sync.changes_conflicting_targets", skipped))
		} else {
			err = io.printf(i18n.Text("engine.sync.changes")) // Idempotency requires a stable no-change result.
		}
	} else {
		err = io.printf(i18n.Text("engine.sync.synchronized_entries"), changed)
	}
	return rep, errors.Join(err, partialError(rep, conflicts, skip))
}

// reestablishLocalBaseline replaces a stale remote lock record occupying a
// now-local entry's directory slot with a fresh local baseline hashed from the
// installed files. Installed files are never written or modified; only the
// in-memory lock is updated, and it is persisted together with the other
// sync changes.
func (e *Engine) reestablishLocalBaseline(newLock *modfile.Lock, sk modfile.ModSkill, rep *Report) {
	dst := e.skillDir(sk.DirName())
	h, err := dirhash.HashDir(dst)
	if err != nil {
		rep.Entries = append(rep.Entries, EntryReport{Name: sk.Name, Directory: sk.DirName(), Action: ActionLocal, Note: i18n.Text("engine.sync.missing") + dst})
		return
	}
	upsertLock(newLock, modfile.LockSkill{Name: sk.Name, Dir: sk.Alias, Dirhash: h})
	rep.Entries = append(rep.Entries, EntryReport{Name: sk.Name, Directory: sk.DirName(), Action: ActionLocal, Note: i18n.Text("engine.sync.baseline_re_established")})
}
