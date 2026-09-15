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
	"github.com/huija/skillmod/internal/fsutil"
	"github.com/huija/skillmod/internal/i18n"
	"github.com/huija/skillmod/internal/modfile"
	repoaddr "github.com/huija/skillmod/internal/repo"
	"github.com/huija/skillmod/internal/resolve"
	"github.com/huija/skillmod/internal/source"
	"github.com/huija/skillmod/internal/ui"
)

// conflict represents an existing installation target whose content does not match.
type conflict struct {
	name string
	dir  string
}

// Get implements skillmod get: resolve, download, validate, install, then write SKILL.mod and SKILL.lock.
// A failure at any step leaves no partially updated state.
func (e *Engine) Get(ctx context.Context, rawAddr, alias string, io IO, options ...MutationOptions) (*Report, error) {
	run := mutationOptions(options)
	defer io.stopProgress()
	addr, err := address.Parse(rawAddr)
	if err != nil {
		return nil, err
	}
	unlock, err := e.lockState()
	if err != nil {
		return nil, err
	}
	defer unlock()
	m, err := e.loadModOrEmpty()
	if err != nil {
		return nil, err
	}
	lock, err := e.loadLock()
	if err != nil {
		return nil, err
	}

	resolved, err := e.resolveGetSkills(ctx, addr, io)
	if err != nil {
		return nil, err
	}
	if alias != "" {
		if err := fsutil.ValidAlias(alias); err != nil {
			return nil, err
		}
	}
	if alias != "" && len(resolved) != 1 {
		return nil, fmt.Errorf("%s", i18n.Text("engine.get.alias_requires_one_skill"))
	}

	entries := make([]getEntry, 0, len(resolved))
	byDir := make(map[string]int, len(resolved))
	for _, result := range resolved {
		name, err := source.SkillNameFromDir(result.mat.contentDir)
		if err != nil {
			return nil, err
		}
		entryAlias := alias
		if entryAlias == name {
			entryAlias = ""
		}
		dir := name
		if entryAlias != "" {
			dir = entryAlias
		}
		src := addr.Repo + subdirSuffix(result.subdir)
		incoming := modfile.ModSkill{Name: name, Source: src, Alias: entryAlias}
		previousDir := vacatedDir(m, incoming, dir)
		for _, skill := range m.Skills {
			dirName := skill.DirName()
			if dirName == dir && sameRemoteSource(skill.Source, src) {
				continue // Idempotent re-get of the same skill.
			}
			if !sameDir(dirName, dir) {
				continue // Distinct spellings that cannot share a directory.
			}
			// Field convention shared with the byDir branch below:
			// Name holds the existing directory spelling and OtherName the
			// incoming one, so the message always reads "existing, incoming".
			other := ""
			if dirName != dir {
				other = dir
			}
			return nil, &NameConflictError{Name: dirName, Existing: skill.Source, Incoming: src, OtherName: other}
		}
		fold := fsutil.FoldKey(dir)
		if previous, exists := byDir[fold]; exists {
			prev := entries[previous]
			if !sameRemoteSource(prev.source, src) || prev.dir != dir {
				other := ""
				if prev.dir != dir {
					other = dir
				}
				return nil, &NameConflictError{Name: prev.dir, Existing: prev.source, Incoming: src, OtherName: other}
			}
		}
		entry := getEntry{mat: result.mat, name: name, dir: dir, source: src, alias: entryAlias, previousDir: previousDir}
		entries = append(entries, entry)
		byDir[fold] = len(entries) - 1
	}

	// Classify targets: install absent or clean old versions, skip matching versions, and flag local modifications as conflicts.
	var conflicts []conflict
	for i := range entries {
		prevHash := ""
		if old := findLock(lock, entries[i].modSkill()); old != nil {
			prevHash = old.Dirhash
		}
		dst := e.skillDir(entries[i].dir)
		action := classifyTarget(dst, entries[i].mat.dirhash, prevHash)
		entries[i].targetResults = append(entries[i].targetResults, TargetReport{Path: dst, Action: action})
		switch action {
		case ActionInstall:
			entries[i].targets = append(entries[i].targets, dst)
		case ActionConflict:
			conflicts = append(conflicts, conflict{name: entries[i].dir, dir: dst})
		}
	}
	io.stopProgress()
	skip, err := resolveConflicts(io, conflicts, ConflictAsk)
	if err != nil {
		return nil, err
	}
	for _, c := range conflicts {
		index := byDir[fsutil.FoldKey(c.name)]
		if skip[c.dir] {
			entries[index].skippedTargets = append(entries[index].skippedTargets, c.dir)
			setGetTargetResult(&entries[index], c.dir, ActionSkip)
		} else {
			entries[index].targets = append(entries[index].targets, c.dir) // Overwrite was selected.
			setGetTargetResult(&entries[index], c.dir, ActionInstall)
		}
	}

	rep := &Report{Action: CommandGet}
	for _, entry := range entries {
		action := EntryStatus(ActionInstall)
		switch {
		case len(entry.targets) == 0 && len(entry.skippedTargets) > 0:
			action = ActionConflict
		case len(entry.targets) == 0:
			action = ActionKeep
		}
		rep.Entries = append(rep.Entries, EntryReport{
			Name: entry.name, Source: entry.source, Version: entry.mat.version,
			Action: action, Note: entry.mat.note, TargetResults: entry.targetResults,
		})
		if note := entry.directoryChangeNote(); note != "" {
			rep.Notes = append(rep.Notes, note)
		}
	}

	if run.DryRun {
		rep.Notes = append(rep.Notes, i18n.Text("engine.dry_run_files_written"))
		return rep, errors.Join(printGetDryRunReport(rep, io), partialError(rep, conflicts, skip))
	}

	for _, entry := range entries {
		skill := entry.modSkill()
		skill.Version = entry.mat.version
		upsertMod(m, skill)
		upsertLock(lock, modfile.LockSkill{
			Name: entry.name, Source: entry.source, Version: entry.mat.version,
			Commit: entry.mat.commit, Dirhash: entry.mat.dirhash, Dir: entry.alias,
		})
	}
	if err := modfile.ValidateMod(m); err != nil {
		return nil, err
	}
	if err := modfile.ValidateLock(lock); err != nil {
		return nil, err
	}

	plans := make([]plannedInstall, 0, len(entries))
	for _, entry := range entries {
		plans = append(plans, plannedInstall{name: entry.dir, contentDir: entry.mat.contentDir, targets: entry.targets})
	}
	// Phase 2 installs first and writes mod and lock only on success; restore old directories if writing fails.
	finalize, err := applyInstallsWithMode(plans, e.Config.InstallMode)
	if err != nil {
		return nil, err
	}
	if err := e.saveState(m, lock); err != nil {
		return nil, errors.Join(err, finalize(false))
	}
	if err := finalize(true); err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if len(entry.targets) > 0 {
			if err := io.printf(i18n.Text("engine.get.installed_skill_mod_skill"), entry.name, entry.mat.version); err != nil {
				return rep, errors.Join(err, partialError(rep, conflicts, skip))
			}
		}
		if note := entry.directoryChangeNote(); note != "" {
			if err := io.printf("%s", note); err != nil {
				return rep, errors.Join(err, partialError(rep, conflicts, skip))
			}
		}
	}
	return rep, partialError(rep, conflicts, skip)
}

func printGetDryRunReport(rep *Report, io IO) error {
	for _, entry := range rep.Entries {
		if err := io.printf(i18n.Text("engine.get.dry_run_entry"), entry.Name, entry.Version, entry.Action); err != nil {
			return err
		}
	}
	return printReportNotes(rep, io)
}

type getEntry struct {
	mat    *materialized
	name   string
	dir    string
	source string
	alias  string
	// previousDir is set when get moves an existing source to another
	// installation directory. The old directory remains managed by prune.
	previousDir    string
	targets        []string
	skippedTargets []string
	targetResults  []TargetReport
}

func setGetTargetResult(entry *getEntry, path string, action TargetStatus) {
	for i := range entry.targetResults {
		if entry.targetResults[i].Path == path {
			entry.targetResults[i].Action = action
			return
		}
	}
}

func (e getEntry) modSkill() modfile.ModSkill {
	return modfile.ModSkill{Name: e.name, Source: e.source, Alias: e.alias}
}

func (e getEntry) directoryChangeNote() string {
	if e.previousDir == "" {
		return ""
	}
	return i18n.Format("engine.get.notice_changing_installation", e.previousDir, e.dir)
}

type resolvedGetSkill struct {
	mat    *materialized
	subdir string
}

// resolveGetSkills supports standalone skills at a repository root and skill
// collections beneath skills/. An explicit subdirectory always wins.
func (e *Engine) resolveGetSkills(ctx context.Context, addr *address.Address, io IO) ([]resolvedGetSkill, error) {
	memo := newOperationMemo(io.Progress)
	if addr.Subdir != "" {
		mat, err := e.resolveAndFetch(ctx, addr.Repo, addr.Subdir, addr.Ref, memo)
		if err == nil {
			return []resolvedGetSkill{{mat: mat, subdir: addr.Subdir}}, nil
		}
		// A single segment is also accepted as a skill-name shorthand when it
		// is not an exact repository subdirectory. Exact paths always win.
		if !strings.Contains(addr.Subdir, "/") {
			candidate, found, lookupErr := e.findSkillByName(ctx, addr.Repo, addr.Subdir, addr.Ref, memo)
			if lookupErr != nil {
				return nil, lookupErr
			}
			if found {
				mat, lookupErr = e.resolveAndFetch(ctx, addr.Repo, candidate.subdir, addr.Ref, memo)
				if lookupErr != nil {
					return nil, lookupErr
				}
				return []resolvedGetSkill{{mat: mat, subdir: candidate.subdir}}, nil
			}
		}
		return nil, err
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
	io.stopProgress()
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

func (e *Engine) findSkillByName(ctx context.Context, repo, name, ref string, memo *operationMemo) (skillCandidate, bool, error) {
	root, err := e.resolveAndFetch(ctx, repo, "", ref, memo)
	var missing *resolve.NotFoundError
	if err != nil && ref != "" && errors.As(err, &missing) {
		// A collection may only tag individual skill subdirectories. Use the
		// default branch for discovery, then resolve the matched path at ref.
		root, err = e.resolveAndFetch(ctx, repo, "", "", memo)
	}
	if err != nil {
		return skillCandidate{}, false, nil
	}
	memo.setProgress(
		i18n.Text("engine.get.discovering_skills"),
		i18n.Text("engine.get.reading_skill_metadata"),
		i18n.Text("engine.get.matching_requested_skill_name"),
	)
	candidates, err := skillCandidates(root.contentDir)
	if err != nil {
		return skillCandidate{}, false, err
	}
	var matches []skillCandidate
	for _, candidate := range candidates {
		if candidate.name == name {
			matches = append(matches, candidate)
		}
	}
	switch len(matches) {
	case 0:
		return skillCandidate{}, false, nil
	case 1:
		return matches[0], true, nil
	default:
		paths := make([]string, len(matches))
		for i, match := range matches {
			paths[i] = match.subdir
		}
		return skillCandidate{}, false, fmt.Errorf(i18n.Text("engine.get.skill_name_matches_multiple"), name, strings.Join(paths, ", "))
	}
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
		return nil, &source.NoSkillMDError{Detail: i18n.Text("engine.get.skill_md_missing_repository")}
	}
	if len(candidates) == 1 || io.Yes {
		return candidates, nil
	}

	displaySubdirs := candidateDisplaySubdirs(root, candidates)
	options := make([]ui.Option, len(candidates))
	for i, candidate := range candidates {
		options[i] = candidate.option(repo, displaySubdirs[i])
	}
	if selector, ok := io.Confirm.(ui.MultiSelector); ok {
		selected, err := selector.ChooseMany(i18n.Format("engine.get.multiple_skills_found_select", repo), options)
		if err != nil {
			return nil, err
		}
		return selectedCandidates(candidates, selected)
	}
	if io.Confirm == nil {
		displayOptions := make([]string, len(candidates))
		for i, candidate := range candidates {
			displayOptions[i] = candidate.displayOption(repo, displaySubdirs[i])
		}
		return nil, &skillCandidatesError{Repo: repo, Candidates: displayOptions}
	}
	var selected []skillCandidate
	for i, candidate := range candidates {
		prompt := i18n.Format("engine.get.confirm_install", candidate.name, candidateAddress(repo, displaySubdirs[i]), candidate.description)
		confirmed, err := io.Confirm.Confirm(prompt)
		if err != nil {
			return nil, err
		}
		if confirmed {
			selected = append(selected, candidate)
		}
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("%s", i18n.Text("engine.get.no_skills_selected"))
	}
	return selected, nil
}

func selectedCandidates(candidates []skillCandidate, indices []int) ([]skillCandidate, error) {
	seen := make(map[int]bool, len(indices))
	selected := make([]skillCandidate, 0, len(indices))
	for _, index := range indices {
		if index < 0 || index >= len(candidates) || seen[index] {
			return nil, fmt.Errorf("%s", i18n.Text("engine.get.no_skills_selected"))
		}
		seen[index] = true
		selected = append(selected, candidates[index])
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("%s", i18n.Text("engine.get.no_skills_selected"))
	}
	return selected, nil
}

func (c skillCandidate) option(repo, displaySubdir string) ui.Option {
	description := c.description
	if description == "" {
		description = i18n.Text("engine.get.description")
	}
	return ui.Option{
		Label:       c.name,
		Description: description,
		Detail:      candidateAddress(repo, displaySubdir),
	}
}

func (c skillCandidate) displayOption(repo, displaySubdir string) string {
	option := c.option(repo, displaySubdir)
	return i18n.Format("engine.get.option_format", option.Label, option.Description, option.Detail)
}

func candidateDisplaySubdirs(root string, candidates []skillCandidate) []string {
	nameCounts := make(map[string]int, len(candidates))
	for _, candidate := range candidates {
		nameCounts[candidate.name]++
	}
	displaySubdirs := make([]string, len(candidates))
	for i, candidate := range candidates {
		displaySubdirs[i] = candidate.subdir
		if candidate.subdir == candidate.name {
			continue // Already the shortest exact path, even if names repeat.
		}
		if nameCounts[candidate.name] != 1 || !validDirName(candidate.name) {
			continue
		}
		// A root-level path with the same name would win exact-path resolution,
		// so only advertise the shorthand when that path does not exist.
		if _, err := os.Lstat(filepath.Join(root, candidate.name)); errors.Is(err, fs.ErrNotExist) {
			displaySubdirs[i] = candidate.name
		}
	}
	return displaySubdirs
}

func skillCandidates(root string) ([]skillCandidate, error) {
	var candidates []skillCandidate
	addCandidate := func(dir, subdir string) error {
		if !hasSkillManifest(dir) {
			return nil
		}
		if strings.Contains(subdir, "@") {
			return fmt.Errorf(i18n.Text("address.subdirectory_at_sign"), subdir)
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
	return i18n.Format("engine.get.multiple_skills_found_rerun", e.Repo, strings.Join(e.Candidates, "\n  "))
}

func candidateAddress(repo, subdir string) string {
	// Bare HTTPS addresses are accepted by the CLI, so omit the redundant
	// transport prefix in commands shown to users. Keep the original repo for
	// resolution, storage, and identity comparisons.
	repo = strings.TrimPrefix(repo, "https://")
	if subdir == "" {
		return "skillmod get " + repo
	}
	return "skillmod get " + repo + subdirSuffix(subdir)
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
	return aSubdir == bSubdir && repoaddr.Identity(aRepo) == repoaddr.Identity(bRepo)
}

func upsertMod(m *modfile.Mod, e modfile.ModSkill) {
	for i := range m.Skills {
		existing := &m.Skills[i]
		if sameModEntry(*existing, e) {
			*existing = e
			return
		}
	}
	m.Skills = append(m.Skills, e)
}

func sameModEntry(a, b modfile.ModSkill) bool {
	if a.Source != "" || b.Source != "" {
		return a.Source != "" && b.Source != "" && sameRemoteSource(a.Source, b.Source)
	}
	return a.Local == b.Local && a.Name == b.Name
}

// vacatedDir returns the installation directory a re-get vacates: the first
// existing declaration with the same source identity whose directory differs
// from dir. It mirrors upsertMod, which replaces that same first entry, so the
// "changing installation directory" notice always names the directory that is
// actually abandoned. It returns "" when nothing moves.
func vacatedDir(m *modfile.Mod, incoming modfile.ModSkill, dir string) string {
	for _, skill := range m.Skills {
		if !sameModEntry(skill, incoming) {
			continue
		}
		if !sameDir(skill.DirName(), dir) {
			return skill.DirName()
		}
		return "" // An entry already occupies the target directory.
	}
	return ""
}

func subdirSuffix(subdir string) string {
	if subdir == "" {
		return ""
	}
	return "//" + subdir
}

// sameDir reports whether two installation-directory spellings denote the
// same portable directory. It must not be used as a skill identity comparison.
func sameDir(a, b string) bool {
	return fsutil.FoldKey(a) == fsutil.FoldKey(b)
}

func validDirName(s string) bool {
	return fsutil.ValidAlias(s) == nil
}

// classifyTarget selects install for absent or clean old content, keep for matching content, or conflict for local modifications.
// prevHash is the previous lock hash; matching content is a clean old installation that can be overwritten without losing user data.
func classifyTarget(dst, wantHash, prevHash string) TargetStatus {
	h, err := dirhash.HashDir(dst)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ActionInstall
		}
		// An existing but empty directory holds nothing to preserve and can
		// be installed over safely. Any other unhashable content (a symlink,
		// permission errors) stems from local modifications and must not be
		// overwritten silently, so it is treated as a conflict.
		if entries, readErr := os.ReadDir(dst); readErr == nil && len(entries) == 0 {
			return ActionInstall
		}
		return ActionConflict
	}
	if h == wantHash {
		return ActionKeep
	}
	if prevHash != "" && h == prevHash {
		return ActionInstall
	}
	return ActionConflict
}
