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
	"sort"
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

// GetOptions controls one get run.
type GetOptions struct {
	All    bool // install every skill the repository publishes without asking
	DryRun bool
}

func getOptions(options []GetOptions) GetOptions {
	if len(options) == 0 {
		return GetOptions{}
	}
	return options[0]
}

// Get implements skillmod get: resolve, download, validate, install, then write SKILL.mod and SKILL.lock.
// A failure at any step leaves no partially updated state.
func (e *Engine) Get(ctx context.Context, rawAddr, alias string, io IO, options ...GetOptions) (*Report, error) {
	run := getOptions(options)
	defer io.stopProgress()
	addr, err := address.Parse(rawAddr)
	if err != nil {
		return nil, err
	}
	if alias != "" {
		if err := fsutil.ValidAlias(alias); err != nil {
			return nil, err
		}
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

	resolved, err := e.resolveGetSkills(ctx, addr, io, run.All)
	if err != nil {
		if hint := shortAddressResolutionHint(addr); hint != nil {
			err = errors.Join(err, hint)
		}
		return nil, err
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

// shortAddressResolutionHint names the default host a bare owner/repo was
// extended with, for the run whose fetch then failed. The failure names a URL
// the raw input never contained, so a user who meant a local path needs the
// resolution stated. The normalized address is echoed rather than the raw
// input, which Redact would replace with a placeholder. An address that was
// never completed has nothing to state and returns nil.
func shortAddressResolutionHint(addr *address.Address) error {
	if !addr.Completed {
		return nil
	}
	return fmt.Errorf("%s", i18n.Format("engine.get.short_address_resolved", addr.Repo))
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
func (e *Engine) resolveGetSkills(ctx context.Context, addr *address.Address, io IO, all bool) ([]resolvedGetSkill, error) {
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
	candidates, err := chooseSkillCandidates(root.contentDir, addr.Repo, io, all)
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
		// Report the repository's skills rather than the missing subdirectory:
		// the argument was a skill name, so the names it could have matched are
		// what the caller needs next.
		if len(candidates) == 0 {
			return skillCandidate{}, false, nil
		}
		names := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			names = append(names, candidate.name)
		}
		return skillCandidate{}, false, fmt.Errorf(i18n.Text("engine.get.no_skill_named"),
			name, repoaddr.Identity(repo), strings.Join(names, ", "))
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
	origin      candidateOrigin
}

// candidateOrigin ranks the root a candidate was found under. A repository can
// publish the same skill under more than one conventional root — skills/ and an
// agent mirror such as .openclaw/skills is the common case — and the two would
// install into the same directory under the same name, so discovery keeps one of
// them. The ranking is the one a reader of the repository expects: the
// conventional skills/ collection, then a root-level directory, then the
// agent-specific collections, which are mirrors maintained for one agent rather
// than the repository's own layout.
type candidateOrigin int

const (
	originRepositoryRoot candidateOrigin = iota // the repository itself is one skill
	originCollection
	originRootDirectory
	originAgentCollection
)

// takesPrecedenceOver reports whether this root outranks another, so the copy
// a repository keeps is the one from the layout a reader looks at first.
func (o candidateOrigin) takesPrecedenceOver(other candidateOrigin) bool {
	return o < other
}

// chooseSkillCandidates lets an interactive caller choose one or more skills.
// --all accepts the full discovered collection without asking, and a single
// candidate needs no choice at all. --yes is deliberately not consulted: it
// answers the confirmation that follows, while --all is what states the set,
// which is the same split remove and share use.
func chooseSkillCandidates(root, repo string, io IO, all bool) ([]skillCandidate, error) {
	candidates, err := skillCandidates(root)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, &source.NoSkillMDError{Detail: i18n.Text("engine.get.skill_md_missing_repository")}
	}
	if len(candidates) == 1 || all {
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

// skillCandidates discovers the skills a repository publishes. Discovery order
// is the repository root itself (a standalone skill), the conventional skills/
// collection, any agent-named hidden collection such as .agents/skills or
// .claude/skills, and the root-level directories as well. Those four are always
// scanned, in that order, because a repository that publishes a collection can
// still leave a skill sitting in another root directory, and a skill nobody
// asked for is what discovery is for; duplicates are collapsed. An explicit
// subdirectory always wins over discovery, so a repository only has to look
// conventional to users who never type a path.
func skillCandidates(root string) ([]skillCandidate, error) {
	var candidates []skillCandidate
	// A skill name is the identity an installation directory and a //name address
	// both use, so the same skill published under two roots cannot both be
	// offered: they would install into one place under one name. The key pairs
	// the folded name with the root it was found under, because two skills that
	// share a name inside one collection are two skills, not a duplicate — they
	// stay to be reported as the ambiguity they are. The map holds the position
	// of the copy currently kept, so a copy from a higher-precedence root
	// replaces it where the first one stood.
	byName := map[string]int{}
	seenSubdir := map[string]bool{}
	addCandidate := func(dir, subdir string, origin candidateOrigin) error {
		if seenSubdir[subdir] {
			return nil
		}
		seenSubdir[subdir] = true
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
		key := fsutil.FoldKey(metadata.Name)
		if at, duplicate := byName[key]; duplicate {
			switch {
			case origin.takesPrecedenceOver(candidates[at].origin):
				// A copy from the higher-precedence root replaces the one kept,
				// in the position that one had taken.
				candidates[at] = skillCandidate{subdir: subdir, name: metadata.Name, description: metadata.Description, origin: origin}
			case candidates[at].origin != origin:
				// The kept copy's root outranks this one, so this copy is the
				// duplicate and is dropped.
			default:
				// Neither outranks: the same name inside one collection, or two
				// agent mirrors nobody ranks. Two skills rather than a duplicate,
				// so both stay to be reported as the ambiguity they are.
				candidates = append(candidates, skillCandidate{subdir: subdir, name: metadata.Name, description: metadata.Description, origin: origin})
			}
			return nil
		}
		byName[key] = len(candidates)
		candidates = append(candidates, skillCandidate{subdir: subdir, name: metadata.Name, description: metadata.Description, origin: origin})
		return nil
	}
	if err := addCandidate(root, "", originRepositoryRoot); err != nil {
		return nil, err
	}
	for _, collection := range collectionRoots(root) {
		if err := walkCollection(root, collection.dir, collection.origin, addCandidate); err != nil {
			return nil, err
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if err := addCandidate(filepath.Join(root, entry.Name()), entry.Name(), originRootDirectory); err != nil {
			return nil, err
		}
	}
	return candidates, nil
}

// collectionDirName is the directory name a skill collection is published
// under, both bare (skills/) and inside an agent directory (.agents/skills).
const collectionDirName = "skills"

// collectionRoot is one directory a collection is published under, with the
// origin its skills carry.
type collectionRoot struct {
	dir    string
	origin candidateOrigin
}

// collectionRoots lists the collection directories a repository may publish
// under, in discovery order: the conventional skills/ directory and every
// hidden agent-named directory that holds one, such as .agents/skills,
// .claude/skills, or .agent/skills. A directory under a hidden one is a mirror
// kept for a single agent, so its skills carry the lower origin and lose to a
// same-named skill in the conventional collection.
func collectionRoots(root string) []collectionRoot {
	var roots []collectionRoot
	for _, pattern := range []string{
		filepath.Join(root, collectionDirName),
		filepath.Join(root, ".*", collectionDirName),
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue // A malformed pattern is a programming error, not user input.
		}
		sort.Strings(matches)
		for _, match := range matches {
			origin := originCollection
			if strings.HasPrefix(filepath.Base(filepath.Dir(match)), ".") {
				origin = originAgentCollection
			}
			roots = append(roots, collectionRoot{dir: match, origin: origin})
		}
	}
	return roots
}

// walkCollection adds every skill under one collection directory at any depth,
// stopping at a directory that is itself a skill. The origin every candidate
// carries is the one the collection itself has, so a skill inside an agent
// directory never outranks the same skill under skills/.
func walkCollection(root, collection string, origin candidateOrigin, addCandidate func(dir, subdir string, origin candidateOrigin) error) error {
	return filepath.WalkDir(collection, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() || path == collection {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if err := addCandidate(path, filepath.ToSlash(rel), origin); err != nil {
			return err
		}
		if hasSkillManifest(path) {
			return filepath.SkipDir
		}
		return nil
	})
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
			e.Agents = existing.Agents
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
