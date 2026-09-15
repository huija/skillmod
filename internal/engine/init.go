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
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/huija/skillmod/internal/dirhash"
	"github.com/huija/skillmod/internal/fsutil"
	"github.com/huija/skillmod/internal/i18n"
	"github.com/huija/skillmod/internal/modfile"
	repoaddr "github.com/huija/skillmod/internal/repo"
	"github.com/huija/skillmod/internal/resolve"
	"github.com/huija/skillmod/internal/source"
	"github.com/huija/skillmod/internal/store"
)

// Init implements skillmod init by scanning existing skills and drafting SKILL.mod.
// It only reads existing skill files, refuses to run when SKILL.mod exists, and backs up and rebuilds with --force.
func (e *Engine) Init(ctx context.Context, force bool, io IO, options ...MutationOptions) (*Report, error) {
	run := mutationOptions(options)
	unlock, err := e.lockState()
	if err != nil {
		return nil, err
	}
	defer unlock()
	modPath := filepath.Join(e.manifestRoot(), modfile.ModFileName)
	if _, err := os.Stat(modPath); err == nil && !force {
		return nil, fmt.Errorf(i18n.Text("engine.init.already_exists"), modPath)
	}

	legacy, legacyErr := e.legacySkills()
	if legacyErr != nil {
		legacy = nil
	}
	defer io.stopProgress()

	// init is the migration and discovery entry point; it scans the installation
	// directory even when no declaration currently installs into it.
	type scanned struct {
		name    string // SKILL.md frontmatter name; use the directory name as a placeholder on parse failure
		dirName string
		dir     string
		note    string
		hash    string
	}
	// The installation directory, not the frontmatter name, identifies an entry.
	seen := map[string]*scanned{}
	var skipped []string
	base := e.skillsDir()
	dents, err := os.ReadDir(base)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		for _, d := range dents {
			dir := filepath.Join(base, d.Name())
			st, statErr := os.Stat(dir)
			if statErr != nil {
				skipped = append(skipped, i18n.Format("engine.init.unreadable_broken_directory", dir, statErr))
				continue
			}
			if !st.IsDir() {
				continue
			}
			if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
				continue
			}
			s := &scanned{dirName: d.Name(), dir: dir}
			name, err := source.SkillNameFromDir(dir)
			if err != nil {
				// Use the directory name so an invalid SKILL.md can still be identified,
				// but only when it can actually serve as an installation directory.
				if nameErr := fsutil.ValidName(d.Name()); nameErr != nil {
					skipped = append(skipped, i18n.Format("engine.init.init", dir, nameErr))
					continue
				}
				s.name = d.Name()
				s.note = i18n.Text("engine.init.failed_parse_skill_md")
			} else {
				s.name = name
				if s.dirName != s.name {
					if aliasErr := fsutil.ValidAlias(s.dirName); aliasErr != nil {
						skipped = append(skipped, i18n.Format("engine.init.init", dir, aliasErr))
						continue
					}
				}
			}
			h, err := dirhash.HashDir(dir)
			if err != nil {
				skipped = append(skipped, i18n.Format("engine.init.init", dir, err))
				continue
			}
			s.hash = h
			seen[s.dirName] = s
		}
	}
	// Spellings that differ only in letter case map to one installation
	// directory on Windows and macOS; report them instead of silently merging.
	folded := map[string]string{}
	for _, s := range seen {
		key := fsutil.FoldKey(s.dirName)
		if prev, ok := folded[key]; ok {
			return nil, fmt.Errorf(i18n.Text("engine.init.init_found_skills_differ"), prev, seen[prev].dir, s.dirName, s.dir)
		}
		folded[key] = s.dirName
	}

	// Match sources with one batched ls-remote per known source rather than one network request per skill.
	type srcRefs struct {
		repo string
		refs *resolve.Refs
	}
	memo := newOperationMemo(io.Progress)
	e.loadRefsBestEffort(ctx, e.Config.KnownSources, memo, 15*time.Second)
	var sources []srcRefs
	for _, repo := range e.Config.KnownSources {
		refs, err := e.refs(ctx, repo, memo)
		if err != nil {
			if writeErr := io.printf(i18n.Text("engine.init.notice_failed_match_source"), repoaddr.Redact(repo), err); writeErr != nil {
				return nil, writeErr
			}
			continue
		}
		sources = append(sources, srcRefs{repo, refs})
	}

	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)

	m := &modfile.Mod{SchemaVersion: modfile.SchemaVersion}
	lock, err := e.loadLock()
	if err != nil {
		return nil, err
	}
	rep := &Report{Action: CommandInit}
	if legacyErr != nil {
		rep.Notes = append(rep.Notes, legacyErr.Error())
	}
	var locator *store.SnapshotLocator
	if e.Store != nil {
		locator = e.Store.NewSnapshotLocator()
	}
	importer := &legacyImporter{engine: e, records: make(map[string]legacySkill), ambiguous: make(map[string]string), memo: memo}
	for _, dirName := range names {
		name := seen[dirName].name
		dirRecord, hasDirRecord := legacy[dirName]
		nameRecord, hasNameRecord := legacy[name]
		// A legacy map normally uses the published name as its key, while an
		// alias uses the installation directory. Prefer the directory key for
		// an exact slot match; if both keys exist with different records, do
		// not guess which source belongs to the installed files.
		switch {
		case hasDirRecord && hasNameRecord && dirName != name && dirRecord != nameRecord:
			importer.ambiguous[dirName] = i18n.Format("engine.init.ambiguous_previous_installer", dirName, name)
		case hasDirRecord:
			importer.records[dirName] = dirRecord
		case hasNameRecord:
			importer.records[dirName] = nameRecord
		}
	}
	unresolved := 0
	if len(skipped) > 0 {
		rep.Notes = append(rep.Notes, i18n.Text("engine.init.following_directories_skipped"))
		for _, msg := range skipped {
			rep.Notes = append(rep.Notes, "  "+msg)
		}
	}
	for _, dirName := range names {
		s := seen[dirName]
		name := s.name
		alias := ""
		if s.dirName != name {
			alias = s.dirName
		}
		entry := EntryReport{Name: name}
		matched, matchedLock, identifyErr := e.identifyInstalled(s.dir, name, alias, s.hash, lock, locator)
		if identifyErr != nil {
			entry.Note = i18n.Format("engine.init.provenance_unrecoverable", identifyErr)
		}
		previous, recorded := importer.records[dirName]
		// An ambiguous legacy slot never contributes provenance. Its diagnostic
		// only describes a local baseline, so it is appended after matching has
		// decided the entry's final action.
		var ambiguity string
		ambiguous := false
		if message, isAmbiguous := importer.ambiguous[dirName]; isAmbiguous {
			recorded, ambiguous, ambiguity = true, true, message
		}
		// A record is unusable when it names no Git repository skillmod can
		// resolve. A "local" record is not one of those: the previous installer
		// already recorded that the skill has no remote origin, so it stays a
		// plain local declaration with no gap to report. Any other unusable record
		// is reported and the search continues, because a known source publishing
		// the same content is a better answer than a local baseline; the entry
		// stays unresolved when none does.
		localOnly := recorded && previous.SourceType == legacySourceLocal
		dropped := false
		if recorded && !ambiguous && !localOnly {
			if _, _, gap := previous.location(); gap != nil {
				entry.Note = appendNote(entry.Note, gap.Error())
				recorded, dropped = false, true
			}
		}
		if matched == nil && recorded && !ambiguous && !localOnly {
			io.setProgress(i18n.Format("engine.init.recovering_installed_skill", name))
			rctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
			matched, matchedLock, err = importer.match(rctx, previous, name, alias, s.hash)
			cancel()
			if err != nil {
				entry.Action = ActionUnresolved
				// A malformed legacy record can contain credentials. Keep the
				// diagnostic useful without echoing its raw source into reports.
				entry.Source = repoaddr.Redact(previous.Source)
				if repo, subdir, locationErr := previous.location(); locationErr == nil {
					entry.Source = repo
					if subdir != "" {
						entry.Source += subdirSuffix(subdir)
					}
				}
				entry.Note = appendNote(entry.Note, err.Error())
				entry.TargetResults = append(entry.TargetResults, TargetReport{
					Path: s.dir, Action: ActionUnverifiable, Note: err.Error(),
				})
				// Preserve a usable declaration and baseline even when the old
				// installer record cannot be verified today. The source is kept in
				// the report so the user can retry or repair it later; treating this
				// one entry as local must not discard all successfully recovered
				// entries from the same import.
				m.Skills = append(m.Skills, modfile.ModSkill{Name: name, Alias: alias, Local: true})
				upsertLock(lock, modfile.LockSkill{Name: name, Dirhash: s.hash, Dir: alias})
				rep.Entries = append(rep.Entries, entry)
				unresolved++
				continue
			}
		}
		for _, sr := range sources {
			if recorded {
				break
			} // Explicit legacy provenance wins over directory-name heuristics.
			if matched != nil {
				break
			}
			var subdir string
			var candidate *resolve.Resolution
			// Monorepo convention: match a <directory-name>/v* tag prefix.
			if r, err := resolve.Resolve(resolve.Request{Repo: sr.repo, Subdir: s.dirName}, sr.refs); err == nil && r.Kind == resolve.KindTag && hasPrefixTag(sr.refs, s.dirName+"/") {
				subdir, candidate = s.dirName, r
			}
			// Single-repository convention: the repository name equals the directory name and has a root tag.
			if candidate == nil && strings.TrimSuffix(path.Base(sr.repo), ".git") == s.dirName {
				if r, err := resolve.Resolve(resolve.Request{Repo: sr.repo}, sr.refs); err == nil && r.Kind == resolve.KindTag {
					candidate = r
				}
			}
			if candidate == nil {
				continue
			}
			rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			mat, matchErr := e.materialize(rctx, sr.repo, subdir, *candidate, "", memo)
			cancel()
			if matchErr != nil {
				entry.Note = appendNote(entry.Note, i18n.Format("engine.init.source_unverified_local", matchErr))
				continue
			}
			if mat.dirhash != s.hash {
				continue
			}
			src := sr.repo
			if subdir != "" {
				src += subdirSuffix(subdir)
			}
			matched = &modfile.ModSkill{Name: name, Source: src, Version: mat.version, Alias: alias}
			matchedLock = &modfile.LockSkill{Name: name, Source: src, Version: mat.version, Commit: mat.commit, Dirhash: mat.dirhash, Dir: alias}
		}
		if matched != nil {
			upsertLock(lock, *matchedLock)
			entry.Source = matched.Source
			entry.Version = matched.Version
			entry.Action = ActionMatched
			if identifyErr != nil {
				// A legacy record or known-source match supersedes a corrupt
				// snapshot provenance lookup; do not report the intermediate
				// failure after recovery has succeeded.
				entry.Note = ""
			}
			if recorded && matchedLock.Dirhash != s.hash {
				entry.Note = appendNote(entry.Note, i18n.Text("engine.init.installed_contents_differ"))
			}
			m.Skills = append(m.Skills, *matched)
		} else {
			// For a local entry, record its name and baseline content dirhash.
			m.Skills = append(m.Skills, modfile.ModSkill{Name: name, Alias: alias, Local: true})
			upsertLock(lock, modfile.LockSkill{Name: name, Dirhash: s.hash, Dir: alias})
			entry.Action = ActionLocal
			switch {
			case ambiguity != "":
				entry.Note = appendNote(entry.Note, ambiguity)
			case dropped:
				// The old installer recorded a source skillmod cannot use and no
				// known source matched, so the entry stays unresolved even though
				// it is kept as a local baseline.
				entry.Action = ActionUnresolved
				unresolved++
			case !recorded && entry.Note == "":
				entry.Note = i18n.Text("engine.init.no_source_record")
			}
		}
		if s.note != "" {
			entry.Note = appendNote(entry.Note, s.note)
		}
		rep.Entries = append(rep.Entries, entry)
	}

	if len(rep.Entries) == 0 {
		rep.Notes = append(rep.Notes, i18n.Text("engine.init.skills_found_generated_empty"))
	}
	if unresolved > 0 {
		rep.Notes = append(rep.Notes, i18n.Format("engine.init.unresolved_sources", unresolved))
	}

	io.stopProgress()
	for _, entry := range rep.Entries {
		summary := entry.Name + ": " + string(entry.Action)
		if entry.Source != "" {
			summary += " " + entry.Source
		}
		if entry.Version != "" {
			summary += " " + entry.Version
		}
		if err := io.printf("  %s", summary); err != nil {
			return rep, err
		}
		if entry.Note != "" {
			if err := io.printf("    %s", entry.Note); err != nil {
				return rep, err
			}
		}
	}
	for _, note := range rep.Notes {
		if err := io.printf("%s", note); err != nil {
			return rep, err
		}
	}

	// Confirm each entry individually so users can reject uncertain provenance.
	if !io.Yes && io.Confirm == nil {
		rep.Notes = append(rep.Notes, i18n.Text("engine.init.confirmed_non_interactive"))
		return rep, fmt.Errorf("%s", i18n.Text("engine.init.init_requires_confirmation"))
	}
	if io.Confirm != nil && !io.Yes {
		var kept []modfile.ModSkill
		var keptEntries []EntryReport
		for i, sk := range m.Skills {
			confirmed, err := io.Confirm.Confirm(i18n.Format("engine.init.accept_entry", sk.Name, rep.Entries[i].Action))
			if err != nil {
				return nil, err
			}
			if confirmed {
				kept = append(kept, sk)
				keptEntries = append(keptEntries, rep.Entries[i])
			}
		}
		m.Skills = kept
		rep.Entries = keptEntries
		// Do not add rejected local entries to the lock.
		keepDirs := map[string]bool{}
		for _, sk := range kept {
			keepDirs[fsutil.FoldKey(sk.DirName())] = true
		}
		var keptLock []modfile.LockSkill
		for _, lk := range lock.Skills {
			if keepDirs[fsutil.FoldKey(lk.InstallDir())] {
				keptLock = append(keptLock, lk)
			}
		}
		lock.Skills = keptLock
	}

	if run.DryRun {
		rep.Notes = append(rep.Notes, i18n.Text("engine.dry_run_files_written"))
		return rep, nil
	}
	// The backup happens at the write phase so --dry-run never overwrites it.
	if _, err := os.Stat(modPath); err == nil {
		if err := copyFile(modPath, modPath+".bak"); err != nil {
			return nil, fmt.Errorf(i18n.Text("engine.init.backup_failed"), err)
		}
	}
	if err := e.saveState(m, lock); err != nil {
		return nil, err
	}
	if err := io.printf(i18n.Text("engine.init.generated_entries"), modPath, len(m.Skills)); err != nil {
		return rep, err
	}
	return rep, io.printf(i18n.Text("engine.init.next_run_skillmod_sync"))
}

func hasPrefixTag(refs *resolve.Refs, prefix string) bool {
	for tag := range refs.Tags {
		if len(tag) > len(prefix) && tag[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}
