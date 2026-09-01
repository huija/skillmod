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
	"path/filepath"
	"strings"

	"github.com/huija/skillmod/internal/address"
	"github.com/huija/skillmod/internal/dirhash"
	"github.com/huija/skillmod/internal/i18n"
	"github.com/huija/skillmod/internal/modfile"
	"github.com/huija/skillmod/internal/resolve"
	"github.com/huija/skillmod/internal/source"
)

// conflict represents an existing installation target whose content does not match (PRD §3.3 rule 5).
type conflict struct {
	name string
	dir  string
}

// resolveConflicts applies interactive or non-interactive conflict rules and returns directories to keep and skip.
// The PRD defines overwrite, keep and skip, or abort; non-interactive operation without --yes aborts.
func resolveConflicts(io IO, conflicts []conflict) (skip map[string]bool, err error) {
	skip = map[string]bool{}
	if len(conflicts) == 0 {
		return skip, nil
	}
	if io.Confirm != nil {
		for _, c := range conflicts {
			choice := io.Confirm.Choose(
				i18n.Format("Conflict: %s exists and its contents do not match the lock (it may have local changes)", c.dir),
				[]string{
					i18n.Text("overwrite"),
					i18n.Text("keep and skip"),
					i18n.Text("abort"),
				})
			switch choice {
			case 0: // Overwrite.
			case 1:
				skip[c.dir] = true
			default:
				return nil, fmt.Errorf("%s", i18n.Text("aborted by user"))
			}
		}
		return skip, nil
	}
	if io.Yes {
		for _, c := range conflicts {
			skip[c.dir] = true
			io.printf(i18n.Text("conflict (--yes automatically kept and skipped): %s"), c.dir)
		}
		return skip, nil
	}
	msg := i18n.Text("Conflicts detected (targets exist and their contents do not match the lock):")
	for _, c := range conflicts {
		msg += "\n  " + c.dir
	}
	return nil, fmt.Errorf("%s\n%s", msg, i18n.Text("Review the conflicts and retry interactively, or use --yes to keep and skip them automatically"))
}

// Get implements skillmod get: resolve, download, validate, install, then write SKILL.mod and SKILL.lock.
// A failure at any step leaves no partially updated state (PRD §3.2 rule 6).
func (e *Engine) Get(ctx context.Context, rawAddr, alias string, io IO) (*Report, error) {
	addr, err := address.Parse(rawAddr)
	if err != nil {
		return nil, err
	}
	m, err := e.loadModOrEmpty()
	if err != nil {
		return nil, err
	}
	lock := e.loadLock()

	resolved, err := e.resolveGetSkills(ctx, addr, io)
	if err != nil {
		return nil, err
	}
	if alias != "" && !validDirName(alias) {
		return nil, fmt.Errorf(i18n.Text("alias %q contains invalid characters (only letters, digits, '.', '_', and '-' are allowed)"), alias)
	}
	if alias != "" && len(resolved) != 1 {
		return nil, fmt.Errorf("%s", i18n.Text("--alias can only be used when exactly one skill is selected"))
	}

	entries := make([]getEntry, 0, len(resolved))
	byDir := make(map[string]int, len(resolved))
	for _, result := range resolved {
		name, err := source.SkillNameFromDir(result.mat.contentDir)
		if err != nil {
			return nil, err
		}
		dir := name
		if alias != "" {
			dir = alias
		}
		src := addr.Repo + subdirSuffix(result.subdir)
		for _, skill := range m.Skills {
			if skill.DirName() == dir && !sameRemoteSource(skill.Source, src) {
				return nil, &NameConflictError{Name: skill.Name, Existing: skill.Source, Incoming: src}
			}
		}
		if previous, exists := byDir[dir]; exists && !sameRemoteSource(entries[previous].source, src) {
			return nil, &NameConflictError{Name: name, Existing: entries[previous].source, Incoming: src}
		}
		entry := getEntry{mat: result.mat, name: name, dir: dir, source: src}
		entries = append(entries, entry)
		byDir[dir] = len(entries) - 1
	}

	// Classify targets: install absent or clean old versions, skip matching versions, and flag local modifications as conflicts.
	adapters, err := e.adapters()
	if err != nil {
		return nil, err
	}
	var conflicts []conflict
	for i := range entries {
		prevHash := ""
		if old := findLock(lock, entries[i].name); old != nil {
			prevHash = old.Dirhash
		}
		for _, adapter := range adapters {
			dst := adapterDir(adapter, e.Root, entries[i].dir)
			switch classifyTarget(dst, entries[i].mat.dirhash, prevHash) {
			case "install":
				entries[i].targets = append(entries[i].targets, dst)
			case "conflict":
				conflicts = append(conflicts, conflict{name: entries[i].dir, dir: dst})
			}
		}
	}
	skip, err := resolveConflicts(io, conflicts)
	if err != nil {
		return nil, err
	}
	for _, c := range conflicts {
		if !skip[c.dir] {
			index := byDir[c.name]
			entries[index].targets = append(entries[index].targets, c.dir) // Overwrite was selected.
		}
	}

	rep := &Report{Action: "get"}
	for _, entry := range entries {
		rep.Entries = append(rep.Entries, EntryReport{
			Name: entry.name, Source: entry.source, Version: entry.mat.version,
			Action: "install", Note: entry.mat.note, Targets: entry.targets,
		})
	}

	if io.DryRun {
		rep.Notes = append(rep.Notes, i18n.Text("dry-run: no files were written"))
		return rep, nil
	}

	plans := make([]plannedInstall, 0, len(entries))
	for _, entry := range entries {
		plans = append(plans, plannedInstall{name: entry.dir, contentDir: entry.mat.contentDir, targets: entry.targets})
	}
	// Phase 2 installs first and writes mod and lock only on success; restore old directories if writing fails.
	finalize, err := applyInstalls(plans)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		entryAlias := ""
		if alias != "" {
			entryAlias = alias
		}
		upsertMod(m, modfile.ModSkill{
			Name: entry.name, Source: entry.source, Version: entry.mat.version, Alias: entryAlias,
		})
		upsertLock(lock, modfile.LockSkill{
			Name: entry.name, Source: entry.source, Version: entry.mat.version,
			Commit: entry.mat.commit, Dirhash: entry.mat.dirhash,
		})
	}
	if err := modfile.SaveMod(e.Root, m); err != nil {
		finalize(false)
		return nil, err
	}
	if err := modfile.SaveLock(e.Root, lock); err != nil {
		finalize(false)
		return nil, err
	}
	if err := finalize(true); err != nil {
		return nil, err
	}
	for _, entry := range entries {
		io.printf(i18n.Text("installed %s %s; SKILL.mod and SKILL.lock were updated"), entry.name, entry.mat.version)
	}
	return rep, nil
}

type getEntry struct {
	mat     *materialized
	name    string
	dir     string
	source  string
	targets []string
}

type resolvedGetSkill struct {
	mat    *materialized
	subdir string
}

// resolveGetSkills supports standalone skills at a repository root and skill
// collections beneath skills/. An explicit subdirectory always wins.
func (e *Engine) resolveGetSkills(ctx context.Context, addr *address.Address, io IO) ([]resolvedGetSkill, error) {
	memo := refsMemo{}
	if addr.Subdir != "" {
		mat, err := e.resolveAndFetch(ctx, addr.Repo, addr.Subdir, addr.Ref, memo)
		if err != nil {
			return nil, err
		}
		return []resolvedGetSkill{{mat: mat, subdir: addr.Subdir}}, nil
	}

	root, err := e.resolveAndFetch(ctx, addr.Repo, "", addr.Ref, memo)
	probedDefault := false
	var missing *resolve.NotFoundError
	if err != nil && addr.Ref != "" && errors.As(err, &missing) {
		// A collection may publish only skills/<name>/vX.Y.Z tags. Resolve the
		// default branch solely to discover the directory, then resolve the
		// requested version against the selected subdirectory below.
		root, err = e.resolveAndFetch(ctx, addr.Repo, "", "", memo)
		probedDefault = true
	}
	if err != nil {
		return nil, err
	}
	candidates, err := chooseSkillCandidates(root.contentDir, addr.Repo, io)
	if err != nil {
		return nil, err
	}
	resolved := make([]resolvedGetSkill, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.subdir == "" && !probedDefault {
			resolved = append(resolved, resolvedGetSkill{mat: root})
			continue
		}
		mat, err := e.resolveAndFetch(ctx, addr.Repo, candidate.subdir, addr.Ref, memo)
		if err != nil {
			return nil, err
		}
		resolved = append(resolved, resolvedGetSkill{mat: mat, subdir: candidate.subdir})
	}
	return resolved, nil
}

// skillCandidate is one discoverable SKILL.md, with the root represented by
// an empty subdirectory.
type skillCandidate struct {
	subdir      string
	name        string
	description string
}

// chooseSkillCandidates lets an interactive caller choose one or more skills.
// --yes explicitly accepts the full discovered collection.
func chooseSkillCandidates(root, repo string, io IO) ([]skillCandidate, error) {
	candidates, err := skillCandidates(root)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, &source.NoSkillMDError{Detail: i18n.Text("SKILL.md is missing from the repository root and every descendant of skills/")}
	}
	if len(candidates) == 1 || io.Yes {
		return candidates, nil
	}

	options := make([]string, len(candidates))
	for i, candidate := range candidates {
		options[i] = candidate.option(repo)
	}
	if selector, ok := io.Confirm.(interface {
		ChooseMany(prompt string, options []string) []int
	}); ok {
		return selectedCandidates(candidates, selector.ChooseMany(i18n.Format("multiple skills found in %s; select one or more", repo), options))
	}
	if io.Confirm == nil {
		return nil, &skillCandidatesError{Repo: repo, Candidates: options}
	}
	var selected []skillCandidate
	for _, candidate := range candidates {
		prompt := i18n.Format("install %s from %s?\n%s", candidate.name, candidateAddress(repo, candidate.subdir), candidate.description)
		if io.Confirm.Confirm(prompt) {
			selected = append(selected, candidate)
		}
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("%s", i18n.Text("no skills selected"))
	}
	return selected, nil
}

func selectedCandidates(candidates []skillCandidate, indices []int) ([]skillCandidate, error) {
	seen := make(map[int]bool, len(indices))
	selected := make([]skillCandidate, 0, len(indices))
	for _, index := range indices {
		if index < 0 || index >= len(candidates) || seen[index] {
			return nil, fmt.Errorf("%s", i18n.Text("no skills selected"))
		}
		seen[index] = true
		selected = append(selected, candidates[index])
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("%s", i18n.Text("no skills selected"))
	}
	return selected, nil
}

func (c skillCandidate) option(repo string) string {
	description := c.description
	if description == "" {
		description = i18n.Text("no description")
	}
	return i18n.Format("%s — %s\n  %s", c.name, description, candidateAddress(repo, c.subdir))
}

func skillCandidates(root string) ([]skillCandidate, error) {
	var candidates []skillCandidate
	addCandidate := func(dir, subdir string) error {
		if !hasSkillManifest(dir) {
			return nil
		}
		metadata, err := source.SkillMetadataFromDir(dir)
		if err != nil {
			return err
		}
		candidates = append(candidates, skillCandidate{subdir: subdir, name: metadata.Name, description: metadata.Description})
		return nil
	}
	if err := addCandidate(root, ""); err != nil {
		return nil, err
	}

	skillsRoot := filepath.Join(root, "skills")
	_, err := os.Stat(skillsRoot)
	if os.IsNotExist(err) {
		return candidates, nil
	}
	if err != nil {
		return nil, err
	}
	err = filepath.WalkDir(skillsRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() || path == skillsRoot {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if err := addCandidate(path, filepath.ToSlash(rel)); err != nil {
			return err
		}
		if hasSkillManifest(path) {
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return candidates, nil
}

func hasSkillManifest(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, "SKILL.md"))
	return err == nil && st.Mode().IsRegular()
}

type skillCandidatesError struct {
	Repo       string
	Candidates []string
}

func (e *skillCandidatesError) Error() string {
	return i18n.Format("multiple skills found in %s; rerun interactively to select one or more, use --yes to install all, or specify one of:\n  %s", e.Repo, strings.Join(e.Candidates, "\n  "))
}

func candidateAddress(repo, subdir string) string {
	if subdir == "" {
		return "skillmod get " + repo
	}
	return "skillmod get " + repo + "//" + subdir
}

func sameRemoteSource(a, b string) bool {
	if a == b {
		return true
	}
	aRepo, aSubdir, err := splitSource(a)
	if err != nil {
		return false
	}
	bRepo, bSubdir, err := splitSource(b)
	if err != nil {
		return false
	}
	return aSubdir == bSubdir && source.RepoIdentity(aRepo) == source.RepoIdentity(bRepo)
}

func upsertMod(m *modfile.Mod, e modfile.ModSkill) {
	for i := range m.Skills {
		if m.Skills[i].Name == e.Name {
			m.Skills[i] = e
			return
		}
	}
	m.Skills = append(m.Skills, e)
}

func subdirSuffix(subdir string) string {
	if subdir == "" {
		return ""
	}
	return "//" + subdir
}

func validDirName(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	for _, c := range s {
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '.' || c == '_' || c == '-'
		if !ok {
			return false
		}
	}
	return true
}

// classifyTarget selects install for absent or clean old content, keep for matching content, or conflict for local modifications.
// prevHash is the previous lock hash; matching content is a clean old installation that can be overwritten without losing user data.
func classifyTarget(dst, wantHash, prevHash string) string {
	h, err := dirhash.HashDir(dst)
	if err != nil {
		return "install"
	}
	if h == wantHash {
		return "keep"
	}
	if prevHash != "" && h == prevHash {
		return "install"
	}
	return "conflict"
}
