// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package engine

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/huija/skillmod/internal/dirhash"
	"github.com/huija/skillmod/internal/fsutil"
	"github.com/huija/skillmod/internal/i18n"
	"github.com/huija/skillmod/internal/modfile"
	"github.com/huija/skillmod/internal/source"
)

// adoptCandidate is one skill directory in the installation directory that
// neither SKILL.mod nor SKILL.lock knows about.
type adoptCandidate struct {
	dirName string // directory name, which becomes the installation directory
	name    string // frontmatter name, or the directory name when it is unreadable
	dir     string
	hash    string // baseline dirhash recorded in the lock
	note    string // extra provenance to report with the entry
}

// reconcileUndeclaredSkills takes stock of the skill directories that exist on
// disk but in neither SKILL.mod nor SKILL.lock — the skills another tool such
// as npx, or a manual copy, put there. With adopt set, each one becomes a
// local declaration, which is what init does for a machine's existing skills.
// Content is never written: an adopted skill keeps exactly the files it has,
// and its lock record is the hash of those files, so a later edit shows up as
// local drift instead of being silently reset. Without adopt the scan only
// leaves a hint naming the flag, so the feature is discoverable from the
// command that would otherwise ignore those skills. A dry run plans the
// adoption but never asks: printing the plan must not block on a prompt, so
// every candidate is listed as pending and the notes say how a real run
// proceeds.
func (e *Engine) reconcileUndeclaredSkills(adopt, dryRun bool, m, newMod *modfile.Mod, lock, newLock *modfile.Lock, io IO, rep *Report) (int, error) {
	candidates, skipped, err := e.undeclaredSkillDirs(m, lock)
	if err != nil {
		return 0, err
	}
	rep.Notes = append(rep.Notes, skipped...)
	if len(candidates) == 0 {
		return 0, nil
	}
	if !adopt {
		hint := i18n.Format("engine.sync.undeclared_skills_found", len(candidates))
		if writeErr := io.printf("%s", hint); writeErr != nil {
			return 0, writeErr
		}
		rep.Notes = append(rep.Notes, hint)
		return 0, nil
	}
	if !dryRun && !io.Yes {
		// The confirmation per skill is the one init asks, for the same reason:
		// provenance is being recorded, and a run with no channel to ask
		// through is told how to state the intent instead of guessing it.
		if io.Confirm == nil {
			return 0, fmt.Errorf("%s", i18n.Format("engine.sync.adopt_needs_confirmation", len(candidates)))
		}
		var kept []adoptCandidate
		for _, candidate := range candidates {
			confirmed, confirmErr := io.Confirm.Confirm(i18n.Format("engine.sync.adopt_confirm", candidate.name, candidate.dir))
			if confirmErr != nil {
				return 0, confirmErr
			}
			if confirmed {
				kept = append(kept, candidate)
			}
		}
		if len(kept) == 0 {
			return 0, fmt.Errorf("%s", i18n.Text("engine.sync.no_skills_adopted"))
		}
		candidates = kept
	}

	adopted := 0
	for _, candidate := range candidates {
		alias := ""
		if candidate.dirName != candidate.name {
			alias = candidate.dirName
		}
		newMod.Skills = append(newMod.Skills, modfile.ModSkill{Name: candidate.name, Alias: alias, Local: true})
		upsertLock(newLock, modfile.LockSkill{Name: candidate.name, Dirhash: candidate.hash, Dir: alias})
		note := i18n.Text("engine.sync.adopted_local_entry")
		if dryRun {
			note = i18n.Text("engine.sync.adopt_dry_run_entry")
		}
		// The action names the adoption rather than the steady state the entry
		// reaches afterwards: a report says what this run did, the next sync
		// sees an ordinary local entry, and a --json consumer can tell a
		// planned adoption from an existing local entry inside one document —
		// which reading the note would have required.
		entry := EntryReport{Name: candidate.name, Directory: candidate.dirName, Action: ActionAdopt, Note: note}
		entry.Note = appendNote(entry.Note, candidate.note)
		rep.Entries = append(rep.Entries, entry)
		if err := io.printf("  %s: %s", candidate.name, entry.Note); err != nil {
			return adopted, err
		}
		adopted++
	}
	if dryRun && !io.Yes {
		// A dry run lists every candidate without asking, so the notes must
		// say how the real run turns the plan into declarations.
		rep.Notes = append(rep.Notes, i18n.Text("engine.sync.adopt_dry_run_confirm"))
	}
	return adopted, nil
}

// undeclaredSkillDirs scans the installation directory for skill directories
// that neither the declaration nor the lock records. Unreadable directories,
// and directories whose name cannot be an installation directory, are reported
// as skipped notes instead of failing the whole sync, which keeps one broken
// skill from blocking every other entry.
func (e *Engine) undeclaredSkillDirs(m *modfile.Mod, lock *modfile.Lock) (candidates []adoptCandidate, skipped []string, err error) {
	base := e.skillsDir()
	dents, readErr := os.ReadDir(base)
	if errors.Is(readErr, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if readErr != nil {
		return nil, nil, readErr
	}
	declared := make(map[string]bool, len(m.Skills))
	for _, skill := range m.Skills {
		declared[fsutil.FoldKey(skill.DirName())] = true
	}
	locked := make(map[string]bool, len(lock.Skills))
	for _, lk := range lock.Skills {
		locked[fsutil.FoldKey(lk.InstallDir())] = true
	}
	folded := make(map[string]string, len(dents))
	for _, dent := range dents {
		dir := filepath.Join(base, dent.Name())
		// os.Stat follows a symlinked skill directory the way init does, so a
		// link that points at real content is adopted rather than skipped.
		st, statErr := os.Stat(dir)
		if statErr != nil || !st.IsDir() {
			continue
		}
		if !hasSkillManifest(dir) {
			continue
		}
		key := fsutil.FoldKey(dent.Name())
		if declared[key] || locked[key] {
			continue // Managed already, by a declaration or a stale lock record.
		}
		if previous, duplicate := folded[key]; duplicate {
			// Two directories that differ only in letter case map to one
			// installation directory on Windows and macOS; adopting both would
			// write a declaration no other machine can materialize.
			return nil, nil, fmt.Errorf(i18n.Text("engine.sync.adopt_case_collision"), previous, dent.Name())
		}
		folded[key] = dent.Name()

		candidate := adoptCandidate{dirName: dent.Name(), dir: dir}
		name, nameErr := source.SkillNameFromDir(dir)
		switch {
		case nameErr == nil:
			candidate.name = name
		case fsutil.ValidName(dent.Name()) == nil:
			// An unreadable SKILL.md is still adopted under the directory name,
			// which is the only identity the filesystem guarantees.
			candidate.name = dent.Name()
			candidate.note = i18n.Text("engine.init.failed_parse_skill_md")
		default:
			skipped = append(skipped, i18n.Format("engine.sync.adopt_skipped", dir, nameErr))
			continue
		}
		hash, hashErr := dirhash.HashDir(dir)
		if hashErr != nil {
			skipped = append(skipped, i18n.Format("engine.sync.adopt_skipped", dir, hashErr))
			continue
		}
		candidate.hash = hash
		candidates = append(candidates, candidate)
	}
	return candidates, skipped, nil
}
