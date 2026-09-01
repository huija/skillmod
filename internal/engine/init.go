// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/huija/skillmod/internal/dirhash"
	"github.com/huija/skillmod/internal/i18n"
	"github.com/huija/skillmod/internal/install"
	"github.com/huija/skillmod/internal/modfile"
	"github.com/huija/skillmod/internal/resolve"
	"github.com/huija/skillmod/internal/source"
)

// Init implements skillmod init by scanning existing skills and drafting SKILL.mod (PRD §3.1).
// It only reads existing skill files, refuses to run when SKILL.mod exists, and backs up and rebuilds with --force.
func (e *Engine) Init(ctx context.Context, force bool, io IO) (*Report, error) {
	modPath := filepath.Join(e.Root, modfile.ModFileName)
	if _, err := os.Stat(modPath); err == nil {
		if !force {
			return nil, fmt.Errorf(i18n.Text("%s already exists\nAdvice: review it, then use --force to regenerate it (the original is backed up as SKILL.mod.bak)"), modPath)
		}
		if err := copyFile(modPath, modPath+".bak"); err != nil {
			return nil, fmt.Errorf(i18n.Text("backup failed: %w"), err)
		}
	}

	// init is the migration and discovery entry point; scan all known platform directories independently of configured installation targets.
	adapters := install.All()

	// Scan first-level subdirectories in each platform's skill directory.
	type scanned struct {
		name    string // SKILL.md frontmatter name; use the directory name as a placeholder on parse failure
		dirName string
		srcDir  string
		note    string
	}
	seen := map[string]*scanned{}
	for _, a := range adapters {
		base := a.SkillsDir(e.Root)
		dents, err := os.ReadDir(base)
		if err != nil {
			continue // The platform directory does not exist.
		}
		for _, d := range dents {
			if !d.IsDir() {
				continue
			}
			dir := filepath.Join(base, d.Name())
			if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
				continue
			}
			s := &scanned{dirName: d.Name(), srcDir: dir}
			name, err := source.SkillNameFromDir(dir)
			if err != nil {
				s.name = d.Name() // Use the directory name as specified by the PRD §3.1 error table.
				s.note = i18n.Text("failed to parse the SKILL.md name; using the directory name as a placeholder—please correct it manually")
			} else {
				s.name = name
			}
			if prev, ok := seen[s.name]; ok {
				prev.note = i18n.Text("a skill with the same name appears in multiple platform directories; merged into one entry")
				continue // Treat matching names as the same skill.
			}
			seen[s.name] = s
		}
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
	lock := e.loadLock()
	rep := &Report{Action: "init"}
	for _, name := range names {
		s := seen[name]
		entry := EntryReport{Name: name}
		var matched *modfile.ModSkill
		for _, sr := range sources {
			// Monorepo convention: match a <directory-name>/v* tag prefix.
			if r, err := resolve.Resolve(resolve.Request{Repo: sr.repo, Subdir: s.dirName}, sr.refs); err == nil && r.Kind == resolve.KindTag && hasPrefixTag(sr.refs, s.dirName+"/") {
				matched = &modfile.ModSkill{Name: name, Source: sr.repo + "//" + s.dirName, Version: r.Version}
				break
			}
			// Single-repository convention: the repository name equals the directory name and has a root tag.
			if strings.TrimSuffix(path.Base(sr.repo), ".git") == s.dirName {
				if r, err := resolve.Resolve(resolve.Request{Repo: sr.repo}, sr.refs); err == nil && r.Kind == resolve.KindTag {
					matched = &modfile.ModSkill{Name: name, Source: sr.repo, Version: r.Version}
					break
				}
			}
		}
		if matched != nil {
			entry.Source = matched.Source
			entry.Version = matched.Version
			entry.Action = "matched"
			m.Skills = append(m.Skills, *matched)
		} else {
			// For a local entry, record its name and baseline content dirhash (PRD §3.1 rule 2).
			h, err := dirhash.HashDir(s.srcDir)
			if err != nil {
				return nil, err
			}
			m.Skills = append(m.Skills, modfile.ModSkill{Name: name, Local: true})
			upsertLock(lock, modfile.LockSkill{Name: name, Dirhash: h})
			entry.Action = "local"
		}
		if s.note != "" {
			entry.Note = s.note
		}
		rep.Entries = append(rep.Entries, entry)
	}

	if len(rep.Entries) == 0 {
		rep.Notes = append(rep.Notes, i18n.Text("no skills found; generated an empty manifest—use skillmod get to add one"))
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
		keepNames := map[string]bool{}
		for _, sk := range kept {
			keepNames[sk.Name] = true
		}
		var keptLock []modfile.LockSkill
		for _, lk := range lock.Skills {
			if keepNames[lk.Name] {
				keptLock = append(keptLock, lk)
			}
		}
		lock.Skills = keptLock
	}

	if io.DryRun {
		rep.Notes = append(rep.Notes, i18n.Text("dry-run: no files were written"))
		return rep, nil
	}
	if err := modfile.SaveMod(e.Root, m); err != nil {
		return nil, err
	}
	if len(lock.Skills) > 0 {
		if err := modfile.SaveLock(e.Root, lock); err != nil {
			return nil, err
		}
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

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}
