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
	"github.com/huija/skillmod/internal/install"
	"github.com/huija/skillmod/internal/modfile"
	"github.com/huija/skillmod/internal/resolve"
	"github.com/huija/skillmod/internal/source"
	"github.com/huija/skillmod/internal/store"
)

// Init implements skillmod init by scanning existing skills and drafting SKILL.mod (PRD §3.1).
// It only reads existing skill files, refuses to run when SKILL.mod exists, and backs up and rebuilds with --force.
func (e *Engine) Init(ctx context.Context, force bool, io IO) (*Report, error) {
	modPath := filepath.Join(e.manifestRoot(), modfile.ModFileName)
	if _, err := os.Stat(modPath); err == nil && !force {
		return nil, fmt.Errorf(i18n.Text("%s already exists\nAdvice: review it, then use --force to regenerate it (the original is backed up as SKILL.mod.bak)"), modPath)
	}

	legacy, legacyErr := e.legacySkills()
	if legacyErr != nil {
		legacy = nil
	}
	defer io.stopProgress()

	// init is the migration and discovery entry point; scan all known platform directories independently of configured installation targets.
	adapters := install.All()

	// Scan first-level subdirectories in each platform's skill directory.
	type scanned struct {
		name    string // SKILL.md frontmatter name; use the directory name as a placeholder on parse failure
		dirName string
		srcDirs []string // every identical installation found across platform adapters
		note    string
		hash    string
	}
	// The installation directory, not frontmatter name, identifies an entry.
	// The same directory is intentionally merged across platform adapters,
	// while two aliases carrying the same published name remain independent.
	seen := map[string]*scanned{}
	var skipped []string
	for _, a := range adapters {
		base := a.SkillsDir(e.Root)
		dents, err := os.ReadDir(base)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, d := range dents {
			dir := filepath.Join(base, d.Name())
			st, statErr := os.Stat(dir)
			if statErr != nil {
				skipped = append(skipped, i18n.Format("%s (unreadable or broken directory link: %v)", dir, statErr))
				continue
			}
			if !st.IsDir() {
				continue
			}
			if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
				continue
			}
			s := &scanned{dirName: d.Name(), srcDirs: []string{dir}}
			name, err := source.SkillNameFromDir(dir)
			if err != nil {
				// Use the directory name as specified by the PRD §3.1 error table,
				// but only when it can actually serve as an installation directory.
				if nameErr := fsutil.ValidName(d.Name()); nameErr != nil {
					skipped = append(skipped, i18n.Format("%s (%v)", dir, nameErr))
					continue
				}
				s.name = d.Name()
				s.note = i18n.Text("failed to parse the SKILL.md name; using the directory name as a placeholder—please correct it manually")
			} else {
				s.name = name
				if s.dirName != s.name {
					if aliasErr := fsutil.ValidAlias(s.dirName); aliasErr != nil {
						skipped = append(skipped, i18n.Format("%s (%v)", dir, aliasErr))
						continue
					}
				}
			}
			h, err := dirhash.HashDir(dir)
			if err != nil {
				skipped = append(skipped, i18n.Format("%s (%v)", dir, err))
				continue
			}
			s.hash = h
			if prev, ok := seen[s.dirName]; ok {
				if prev.hash != h || prev.name != s.name {
					return nil, fmt.Errorf(i18n.Text("cannot import different skills at %s and %s under the same directory name; reconcile them or rename one first"), prev.srcDirs[0], dir)
				}
				prev.srcDirs = append(prev.srcDirs, dir)
				prev.note = appendInitNote(prev.note, i18n.Text("a skill with the same name appears in multiple platform directories; merged into one entry"))
				continue // Treat the same installation directory as one skill.
			}
			seen[s.dirName] = s
		}
	}
	// Spellings that differ only in letter case map to one installation
	// directory on Windows and macOS; report them instead of silently merging.
	folded := map[string]string{}
	for _, s := range seen {
		key := fsutil.FoldKey(s.dirName)
		if prev, ok := folded[key]; ok {
			return nil, fmt.Errorf(i18n.Text("init found skills %q (in %s) and %q (in %s) that differ only in letter case and would map to the same installation directory; rename one of them"), prev, seen[prev].srcDirs[0], s.dirName, s.srcDirs[0])
		}
		folded[key] = s.dirName
	}

	// Match sources with one batched ls-remote per known source rather than one network request per skill.
	type srcRefs struct {
		repo string
		refs *resolve.Refs
	}
	var sources []srcRefs
	for _, repo := range e.Config.KnownSources {
		rctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		refs, err := e.Source.Refs(rctx, repo)
		cancel()
		if err != nil {
			io.printf(i18n.Text("notice: failed to match source %s (%v); related entries will be treated as local"), repo, err)
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
	rep := &Report{Action: "init"}
	if legacyErr != nil {
		rep.Notes = append(rep.Notes, legacyErr.Error())
	}
	memo := newOperationMemo(io.Progress)
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
			importer.ambiguous[dirName] = i18n.Format("ambiguous previous installer records for directory %q and skill name %q; retained as a local baseline", dirName, name)
		case hasDirRecord:
			importer.records[dirName] = dirRecord
		case hasNameRecord:
			importer.records[dirName] = nameRecord
		}
	}
	unresolved := 0
	if len(skipped) > 0 {
		rep.Notes = append(rep.Notes, i18n.Text("the following directories were skipped because they cannot be represented as valid SKILL.mod entries:"))
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
		matched, matchedLock, identifyErr := e.identifyInstalled(s.srcDirs, name, alias, s.hash, lock, locator)
		if identifyErr != nil {
			entry.Note = i18n.Format("installed provenance could not be recovered: %v", identifyErr)
		}
		previous, recorded := importer.records[dirName]
		// An ambiguous legacy slot never contributes provenance. Its diagnostic
		// only describes a local baseline, so it is appended after matching has
		// decided the entry's final action.
		var ambiguity string
		if message, isAmbiguous := importer.ambiguous[dirName]; isAmbiguous {
			recorded = true
			ambiguity = message
		} else if matched == nil && recorded && previous.SourceType != "local" {
			io.setProgress(i18n.Format("recovering installed skill provenance: %s", name))
			rctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
			matched, matchedLock, err = importer.match(rctx, previous, name, alias)
			cancel()
			if err != nil {
				entry.Action = "unresolved"
				entry.Source = previous.Source
				if repo, subdir, locationErr := previous.location(); locationErr == nil {
					entry.Source = source.RepoIdentity(repo)
					if subdir != "" {
						entry.Source += subdirSuffix(subdir)
					}
				}
				entry.Note = appendInitNote(entry.Note, err.Error())
				entry.Targets = append([]string(nil), s.srcDirs...)
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
				entry.Note = appendInitNote(entry.Note, i18n.Format("source could not be verified; kept as local: %v", matchErr))
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
			entry.Action = "matched"
			if identifyErr != nil {
				// A legacy record or known-source match supersedes a corrupt
				// snapshot provenance lookup; do not report the intermediate
				// failure after recovery has succeeded.
				entry.Note = ""
			}
			if recorded && matchedLock.Dirhash != s.hash {
				entry.Note = appendInitNote(entry.Note, i18n.Text("installed contents differ from the recorded source revision; the recorded source version was retained; run skillmod sync to align"))
			}
			m.Skills = append(m.Skills, *matched)
		} else {
			// For a local entry, record its name and baseline content dirhash (PRD §3.1 rule 2).
			m.Skills = append(m.Skills, modfile.ModSkill{Name: name, Alias: alias, Local: true})
			upsertLock(lock, modfile.LockSkill{Name: name, Dirhash: s.hash, Dir: alias})
			entry.Action = "local"
			if ambiguity != "" {
				entry.Note = appendInitNote(entry.Note, ambiguity)
			} else if !recorded && entry.Note == "" {
				entry.Note = i18n.Text("no verifiable source record found; retained as a local baseline")
			}
		}
		if s.note != "" {
			entry.Note = appendInitNote(entry.Note, s.note)
		}
		rep.Entries = append(rep.Entries, entry)
	}

	if len(rep.Entries) == 0 {
		rep.Notes = append(rep.Notes, i18n.Text("no skills found; generated an empty manifest—use skillmod get to add one"))
	}
	if unresolved > 0 {
		rep.Notes = append(rep.Notes, i18n.Format("%d skills had known but unresolved sources; those entries were retained as local baselines and can be retried later", unresolved))
	}

	io.stopProgress()
	for _, entry := range rep.Entries {
		summary := entry.Name + ": " + entry.Action
		if entry.Source != "" {
			summary += " " + entry.Source
		}
		if entry.Version != "" {
			summary += " " + entry.Version
		}
		io.printf("  %s", summary)
		if entry.Note != "" {
			io.printf("    %s", entry.Note)
		}
	}
	for _, note := range rep.Notes {
		io.printf("%s", note)
	}

	// Confirm each entry individually as required by the PRD interaction flow.
	if !io.Yes && io.Confirm == nil {
		rep.Notes = append(rep.Notes, i18n.Text("not confirmed in a non-interactive environment; rerun with --yes to accept all entries"))
		return rep, fmt.Errorf("%s", i18n.Text("init requires confirmation: select each entry interactively or use --yes to accept all"))
	}
	if io.Confirm != nil && !io.Yes {
		var kept []modfile.ModSkill
		var keptEntries []EntryReport
		for i, sk := range m.Skills {
			if io.Confirm.Confirm(i18n.Format("accept entry %s (%s)?", sk.Name, rep.Entries[i].Action)) {
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

	if io.DryRun {
		rep.Notes = append(rep.Notes, i18n.Text("dry-run: no files were written"))
		return rep, nil
	}
	// The backup happens at the write phase so --dry-run never overwrites it.
	if _, err := os.Stat(modPath); err == nil {
		if err := copyFile(modPath, modPath+".bak"); err != nil {
			return nil, fmt.Errorf(i18n.Text("backup failed: %w"), err)
		}
	}
	if err := e.saveMod(m); err != nil {
		return nil, err
	}
	if err := modfile.SaveLock(e.manifestRoot(), lock); err != nil {
		return nil, err
	}
	io.printf(i18n.Text("generated %s (%d entries) without changing the original files"), modPath, len(m.Skills))
	io.printf(i18n.Text("next: run skillmod sync to align the locked state, then commit SKILL.mod and SKILL.lock"))
	return rep, nil
}

func hasPrefixTag(refs *resolve.Refs, prefix string) bool {
	for tag := range refs.Tags {
		if len(tag) > len(prefix) && tag[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

func appendInitNote(existing, note string) string {
	if existing == "" {
		return note
	}
	if note == "" {
		return existing
	}
	return existing + "; " + note
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}
