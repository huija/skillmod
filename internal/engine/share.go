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

	"github.com/huija/skillmod/internal/agents"
	"github.com/huija/skillmod/internal/dirhash"
	"github.com/huija/skillmod/internal/fsutil"
	"github.com/huija/skillmod/internal/i18n"
	"github.com/huija/skillmod/internal/install"
	"github.com/huija/skillmod/internal/modfile"
	"github.com/huija/skillmod/internal/source"
	"github.com/huija/skillmod/internal/store"
	"github.com/huija/skillmod/internal/ui"
)

// ShareOptions controls one share run. Skills and Agents accept explicit
// requests; when both are empty the interactive selection lists what is
// available, mirroring the collection flow of get. Remove is the reverse
// operation — take one or more agents out of the selected skills' agent lists
// — and excludes every linking option.
type ShareOptions struct {
	Skills     []string // selectors: installation directory names or frontmatter names
	All        bool     // share every installed skill without asking
	Agents     []string // agent names to link into; any single-segment name resolves under .<name>/skills
	Remove     []string // agent names to unlink and un-declare
	OnConflict string   // ask (default), overwrite, or skip; see the Conflict constants
}

// shareSkill is one installed skill a share run can link.
type shareSkill struct {
	dirName    string   // installation directory under .agents/skills; also the linked directory name
	name       string   // SKILL.md frontmatter name
	version    string   // lock baseline version; empty for a local declaration
	contentDir string   // desired content before sync installs it; empty uses the managed copy
	agents     []string // agents the entry already declares, which is what an interactive selection opens on
}

// shareTarget is one destination base directory.
type shareTarget struct {
	label string // agent name
	path  string // absolute destination directory
}

// shareDeclaration is one skill's resolved share destinations: the agent names
// the run links into, each with the directory its name resolves to. Recording
// the name is what keeps the declaration portable, so every destination is an
// agent name — a path would not reproduce on another machine — and the
// directories here are derived from those names for this run only.
type shareDeclaration struct {
	targets  []shareTarget // every destination to link in this run, in link order
	declared []string      // agent names to record on the skill's entry
}

// Share links installed skills from the scope's managed skills directory into
// agent directories. Each destination entry is a symlink to the managed copy
// — the same representation get's auto install mode uses — so the managed
// skill's edits are visible through every link at once, and a filesystem that
// forbids symlinks falls back to a byte-preserving copy exactly like get.
//
// The declaration is per skill: each [[skill]] entry's agents list names the
// agents that skill is linked into, and a skill without the field stays only
// in the managed skills directory. This command is get's mirror image — get
// adds an entry to SKILL.mod, share adds agent names to the selected entries
// — so it records what it linked in the manifest, and sync recreates those
// links on a new machine. The declaration names agents only, never paths: an
// agent name locates its directory through the one ".<name>/skills" rule, so
// the record reproduces everywhere.
func (e *Engine) Share(ctx context.Context, options ShareOptions, io IO, options_ ...MutationOptions) (report *Report, err error) {
	run := mutationOptions(options_)
	// Everything answerable from the arguments alone is answered first: a
	// rejected combination, an unknown conflict policy, or an unusable
	// destination is an argument error, and an interactive selection made
	// before one is reported would have been made for nothing.
	if err := e.checkShareOptions(options); err != nil {
		return nil, err
	}
	unlock, err := e.lockState()
	if err != nil {
		return nil, err
	}
	defer unlock()
	// --remove is the declaration's exit: it takes agents out of the selected
	// skills' lists, so it cannot be mixed with a request that links things
	// back in (rejected above). The selected entries keep their managed copies.
	if len(options.Remove) > 0 {
		return e.shareRemove(options, io, run)
	}
	policy := options.OnConflict
	if policy == "" {
		policy = ConflictAsk
	}
	m, err := e.loadModOrEmpty()
	if err != nil {
		return nil, err
	}
	lock, err := e.loadLock()
	if err != nil {
		return nil, err
	}
	listed, err := e.shareableSkills(m, lock)
	if err != nil {
		return nil, err
	}
	if len(listed) == 0 {
		return nil, fmt.Errorf(i18n.Text("engine.share.no_skills_installed"), e.skillsDir())
	}
	skills, err := selectShareSkills(listed, options, io)
	if err != nil {
		return nil, err
	}
	decl, err := e.selectShareTargets(options, skills, io)
	if err != nil {
		return nil, err
	}
	if err := io.printf(i18n.Format("engine.share.plan", len(skills), len(decl.targets))); err != nil {
		return nil, err
	}
	changed, err := e.prepareShareState(m, lock, skills, decl.declared)
	if err != nil {
		return nil, err
	}

	// Phase 1 classifies every destination before anything is written, then
	// resolves conflicts through the one resolver get, sync, and update use.
	rep := &Report{Action: CommandShare}
	installs := make([][]string, len(skills))
	var conflicts []conflict
	for i, sk := range skills {
		declaration := findModSkill(m, sk.dirName)
		entry := EntryReport{Name: sk.name, Source: declaration.Source, Local: declaration.Local,
			Version: declaration.Version, Directory: sk.dirName}
		for _, target := range decl.targets {
			dst := filepath.Join(target.path, sk.dirName)
			action, note, err := e.classifyShareTarget(sk, dst)
			if err != nil {
				return nil, err
			}
			entry.TargetResults = append(entry.TargetResults, TargetReport{Path: dst, Action: action, Note: note})
			switch action {
			case ActionInstall:
				installs[i] = append(installs[i], dst)
			case ActionConflict:
				conflicts = append(conflicts, conflict{name: sk.name, dir: dst})
			}
		}
		rep.Entries = append(rep.Entries, entry)
	}
	skip, err := resolveConflicts(io, conflicts, policy)
	if err != nil {
		return nil, err
	}
	for i := range rep.Entries {
		for j := range rep.Entries[i].TargetResults {
			result := &rep.Entries[i].TargetResults[j]
			if result.Action != ActionConflict {
				continue
			}
			if skip[result.Path] {
				result.Action = ActionSkip
			} else {
				result.Action = ActionInstall // Overwrite was selected.
				installs[i] = append(installs[i], result.Path)
			}
		}
	}

	// Phase 2 applies the installs; conflicts are settled either way.
	var restores []func() error
	var commits []func()
	committed := false
	defer func() {
		if !committed {
			for i := len(restores) - 1; i >= 0; i-- {
				err = errors.Join(err, restores[i]())
			}
		}
	}()
	skipped := 0
	for i, sk := range skills {
		for _, dst := range installs[i] {
			if run.DryRun {
				setShareTargetResult(&rep.Entries[i], dst, ActionInstall, i18n.Text("engine.share.planned_note"))
				continue
			}
			restore, commit, err := e.stageShare(sk, dst)
			if err != nil {
				return nil, err
			}
			restores = append(restores, restore)
			commits = append(commits, commit)
			setShareTargetResult(&rep.Entries[i], dst, ActionInstall, "")
			if err := io.printf(i18n.Format("engine.share.linked", sk.dirName, dst)); err != nil {
				return nil, err
			}
		}
		installed, kept, skippedTargets := 0, 0, 0
		for _, result := range rep.Entries[i].TargetResults {
			switch result.Action {
			case ActionInstall:
				installed++
			case ActionKeep:
				kept++
			case ActionSkip:
				skippedTargets++
				skipped++
			}
		}
		switch {
		case installed > 0 && skippedTargets > 0:
			rep.Entries[i].Action = ActionPartial
		case installed > 0:
			rep.Entries[i].Action = ActionInstall
		case skippedTargets > 0:
			rep.Entries[i].Action = ActionConflict
		case kept > 0:
			rep.Entries[i].Action = ActionKeep
		}
	}
	if run.DryRun {
		rep.Notes = append(rep.Notes, i18n.Text("engine.share.dry_run_note"))
	}
	// Links remain staged until their declaration and cleanup history are
	// saved. A write failure restores every previous destination.
	if changed && !run.DryRun {
		if err := e.saveState(m, lock); err != nil {
			return nil, err
		}
	}
	committed = true
	for _, commit := range commits {
		commit()
	}
	return rep, partialError(rep, conflicts, skip)
}

// prepareShareState adopts only the selected installation slots before any
// links are written. Verified locks and snapshots retain remote provenance;
// otherwise the existing contents become a local baseline, just as in init.
func (e *Engine) prepareShareState(m *modfile.Mod, lock *modfile.Lock, skills []shareSkill, names []string) (bool, error) {
	var locator *store.SnapshotLocator
	if e.Store != nil {
		locator = e.Store.NewSnapshotLocator()
	}
	changed := false
	for _, sk := range skills {
		entry := findModSkill(m, sk.dirName)
		if entry == nil {
			alias := ""
			if sk.dirName != sk.name {
				alias = sk.dirName
			}
			hash, err := dirhash.HashDir(e.skillDir(sk.dirName))
			if err != nil {
				return false, err
			}
			adopted, baseline, err := e.identifyInstalled(e.skillDir(sk.dirName), sk.name, alias, hash, lock, locator)
			if err != nil {
				return false, err
			}
			if adopted == nil {
				adopted = &modfile.ModSkill{Name: sk.name, Alias: alias, Local: true}
				baseline = &modfile.LockSkill{Name: sk.name, Dir: alias, Dirhash: hash}
			}
			adopted.Agents = append([]string(nil), sk.agents...)
			m.Skills = append(m.Skills, *adopted)
			upsertLock(lock, *baseline)
			entry = &m.Skills[len(m.Skills)-1]
			changed = true
		}
		if appendSkillAgents(entry, names) {
			changed = true
		}
		if err := e.checkShareAgents(entry.Agents); err != nil {
			return false, err
		}
		if recordLockAgents(lock, sk.dirName, entry.Agents) {
			changed = true
		}
	}
	if err := modfile.ValidateMod(m); err != nil {
		return false, err
	}
	return changed, modfile.ValidateLock(lock)
}

// checkShareOptions rejects a request that cannot be carried out, reading
// nothing but the arguments. It runs before the state is opened and before any
// prompt, so a misspelled agent name or an unusable destination is reported as
// itself instead of surfacing after the caller has already picked skills and
// destinations. --remove and --agent name the same kind of destination, so
// both are checked here rather than in the branch that consumes them.
func (e *Engine) checkShareOptions(options ShareOptions) error {
	if options.All && len(options.Skills) > 0 {
		return fmt.Errorf("%s", i18n.Text("engine.share.all_exclusive"))
	}
	// --remove edits the agent lists of existing entries; --agent links things
	// back in. The two directions contradict each other.
	if len(options.Remove) > 0 && len(options.Agents) > 0 {
		return fmt.Errorf("%s", i18n.Text("engine.share.remove_exclusive"))
	}
	if len(options.Remove) > 0 && options.OnConflict != "" {
		return fmt.Errorf("%s", i18n.Text("engine.share.remove_conflict_policy"))
	}
	if len(options.Remove) == 0 {
		// --remove does not read the policy, so it is not asked to validate it.
		if err := ValidateConflictPolicy(options.OnConflict); err != nil {
			return err
		}
	}
	if err := e.checkShareAgents(options.Agents); err != nil {
		return err
	}
	return e.checkShareAgents(options.Remove)
}

// checkShareAgents rejects every named agent that cannot be used: a name that
// is not one portable directory segment, or one whose directory equals,
// contains, or is contained in the managed skills directory the scope
// verifies.
func (e *Engine) checkShareAgents(names []string) error {
	for _, name := range names {
		t, err := agents.Resolve(name)
		if err != nil {
			return err
		}
		if err := rejectManagedOverlap(e.skillsDir(), t.Dir(e.Root)); err != nil {
			return err
		}
	}
	return nil
}

// findModSkill returns the declaration entry whose installation directory
// matches, or nil when the manifest does not declare that directory.
func findModSkill(m *modfile.Mod, dirName string) *modfile.ModSkill {
	for i := range m.Skills {
		if fsutil.FoldKey(m.Skills[i].DirName()) == fsutil.FoldKey(dirName) {
			return &m.Skills[i]
		}
	}
	return nil
}

// appendSkillAgents adds each agent name to the entry's list when it is not
// present already, case-insensitively, and reports whether anything was added.
// The list is an accumulation rather than a replacement: --agent names who the
// skill is linked into on top of whoever it is linked into already, and
// --remove is the only way to take one out. Ordering is settled on save.
func appendSkillAgents(entry *modfile.ModSkill, names []string) bool {
	added := false
	for _, name := range names {
		if !hasAgent(entry.Agents, name) {
			entry.Agents = append(entry.Agents, name)
			added = true
		}
	}
	return added
}

// hasAgent reports whether the list already names the agent in any spelling.
// It compares folded so "Claude" and "claude" are one agent. The manifest's
// list and the one a share run carries answer the same question, so they share
// this check.
func hasAgent(names []string, name string) bool {
	fold := fsutil.FoldKey(name)
	for _, existing := range names {
		if fsutil.FoldKey(existing) == fold {
			return true
		}
	}
	return false
}

// shareRemove implements share --remove: for each selected skill it takes down
// the links that mirror the managed copy under the named agents' directories
// and drops those agent names from the skill's declaration entry, so the
// manifest is the single source of the link set. A run names the skills, uses
// --all, or picks from entries shared to the requested agents.
// A destination holding foreign content is left alone, exactly like remove and
// prune leave it. The declaration is saved only after the links are down, so a
// failed run leaves it intact for a retry — the mirror image of Share saving
// the declaration only after its links are up.
func (e *Engine) shareRemove(options ShareOptions, io IO, run MutationOptions) (*Report, error) {
	m, err := e.loadModOrEmpty()
	if err != nil {
		return nil, err
	}
	lock, err := e.loadLock()
	if err != nil {
		return nil, err
	}
	listed, err := e.shareableSkills(m, lock)
	if err != nil {
		return nil, err
	}
	// Missing managed copies can still leave dangling share links and agent
	// declarations behind. Include those slots so unsharing can clean them.
	for _, entry := range m.Skills {
		found := false
		for _, sk := range listed {
			if sameDir(entry.DirName(), sk.dirName) {
				found = true
				break
			}
		}
		if !found {
			listed = append(listed, shareSkill{dirName: entry.DirName(), name: entry.Name,
				version: entry.Version, agents: entry.Agents})
		}
	}
	sort.Slice(listed, func(i, j int) bool { return listed[i].dirName < listed[j].dirName })
	// A name that is not one portable directory segment fails as itself.
	// Whether a usable name is declared is answered per skill below: the same
	// name may be on one entry and absent from another, and only the latter is
	// a no-op to report.
	targets := make([]agents.Target, 0, len(options.Remove))
	seen := make(map[string]bool)
	for _, name := range options.Remove {
		t, err := agents.Resolve(name)
		if err != nil {
			return nil, err
		}
		if err := rejectManagedOverlap(e.skillsDir(), t.Dir(e.Root)); err != nil {
			return nil, err
		}
		if !seen[t.Name] {
			targets = append(targets, t)
			seen[t.Name] = true
		}
	}
	if len(options.Skills) == 0 {
		var matching []shareSkill
		for _, sk := range listed {
			for _, target := range targets {
				if sk.declares(target.Name) {
					matching = append(matching, sk)
					break
				}
			}
		}
		listed = matching
	}
	if len(listed) == 0 {
		return nil, fmt.Errorf(i18n.Text("engine.share.nothing_shared"), agentNames(targets))
	}
	skills, err := selectUnshareSkills(listed, options, io)
	if err != nil {
		return nil, err
	}
	if _, err := e.prepareShareState(m, lock, skills, nil); err != nil {
		return nil, err
	}
	// Every selected skill must declare at least one of the named agents, or
	// the run would silently do nothing; a per-skill diagnostic names the
	// entries that do not.
	for _, sk := range skills {
		entry := findModSkill(m, sk.dirName)
		if entry == nil || !modSkillHasAnyAgent(entry, targets) {
			return nil, fmt.Errorf(i18n.Text("engine.share.not_declared"), sk.dirName, agentNames(targets), declaredAgentList(entry))
		}
		if _, err := e.shareTargetsFor(m, sk.dirName); err != nil {
			return nil, err
		}
	}

	// Classify every destination under the named agents before anything is
	// written. Only links that mirror the managed copy are removed; missing
	// destinations and foreign content are reported and left alone.
	rep := &Report{Action: CommandShare}
	var removable []string
	removableSkills := make(map[string]shareSkill)
	for _, sk := range skills {
		declaration := findModSkill(m, sk.dirName)
		entry := EntryReport{Name: sk.name, Source: declaration.Source, Local: declaration.Local,
			Version: declaration.Version, Directory: sk.dirName, Action: ActionRemove}
		for _, t := range targets {
			dst := filepath.Join(t.Dir(e.Root), sk.dirName)
			if _, statErr := os.Stat(t.Dir(e.Root)); errors.Is(statErr, fs.ErrNotExist) {
				// The agent directory is absent on this machine, so there is
				// nothing to unlink; the declaration entry still goes.
				entry.TargetResults = append(entry.TargetResults, TargetReport{Path: dst, Action: ActionMissing, Note: i18n.Text("engine.share.remove_absent_agent")})
				continue
			}
			action, note, err := e.classifyShareTarget(sk, dst)
			if err != nil {
				return nil, err
			}
			switch action {
			case ActionKeep:
				// A mirrored link goes with the declaration.
				entry.TargetResults = append(entry.TargetResults, TargetReport{Path: dst, Action: ActionRemove, Note: note})
				removable = append(removable, dst)
				removableSkills[dst] = sk
			case ActionInstall:
				// Nothing linked here; the declaration entry still goes.
				entry.TargetResults = append(entry.TargetResults, TargetReport{Path: dst, Action: ActionMissing})
			default:
				// Foreign content: never discarded by an unshare.
				entry.TargetResults = append(entry.TargetResults, TargetReport{Path: dst, Action: ActionKeep, Note: i18n.Text("engine.share.remove_kept_foreign")})
			}
		}
		rep.Entries = append(rep.Entries, entry)
	}
	if err := io.printf(i18n.Format("engine.share.remove_plan", len(skills), len(removable))); err != nil {
		return nil, err
	}
	if run.DryRun {
		rep.Notes = append(rep.Notes, i18n.Text("engine.share.remove_dry_run_note"))
		return rep, nil
	}
	// A destination already gone needs no staging. Existing links remain in
	// backups until their declaration update succeeds.
	var existing []string
	seenPaths := make(map[string]bool)
	for _, path := range removable {
		if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, err
		}
		// Agent directories may alias one another. Resolve only the parent:
		// resolving the entry itself would merge distinct links to the same
		// managed source. Each actual destination is staged exactly once.
		parent, err := resolveSharePath(filepath.Dir(path))
		if err != nil {
			return nil, err
		}
		key := fsutil.FoldKey(filepath.Join(parent, filepath.Base(path)))
		if seenPaths[key] {
			continue
		}
		seenPaths[key] = true
		existing = append(existing, path)
	}
	finalize, err := stageRemovals(existing, func(path string) error {
		if err := rejectManagedOverlap(e.skillsDir(), filepath.Dir(path)); err != nil {
			return err
		}
		action, _, err := e.classifyShareTarget(removableSkills[path], path)
		if err != nil {
			return err
		}
		if action != ActionKeep {
			return fmt.Errorf(i18n.Text("engine.share.destination_changed"), path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// The links are down; now the declaration. An entry left with no agents
	// loses the field entirely rather than keeping an empty list — "not
	// shared" is expressed by the absence of the declaration, not by a list
	// with nothing in it. The lock follows, so a later prune does not try to
	// clean links this run already took down.
	for _, sk := range skills {
		entry := findModSkill(m, sk.dirName)
		removeSkillAgents(entry, targets)
		setLockAgents(lock, sk.dirName, removeAgentNames(recordedAgents(lock, sk.dirName), targets))
	}
	if err := e.saveState(m, lock); err != nil {
		return nil, errors.Join(err, finalize(false))
	}
	if err := finalize(true); err != nil {
		return rep, err
	}
	return rep, io.printf(i18n.Format("engine.share.remove_done", len(skills), len(targets), len(removable)))
}

// selectUnshareSkills never assumes a deletion set or opens it checked. Names
// and --all state the set; otherwise only skills shared to the requested agents
// are offered for an explicit interactive selection.
func selectUnshareSkills(listed []shareSkill, options ShareOptions, io IO) ([]shareSkill, error) {
	if options.All {
		return listed, nil
	}
	if len(options.Skills) > 0 {
		return matchShareSkills(listed, options.Skills)
	}
	if io.Confirm == nil {
		return nil, fmt.Errorf("%s", i18n.Text("engine.share.remove_needs_skills"))
	}
	choices := make([]ui.Option, len(listed))
	for i, sk := range listed {
		choices[i] = sk.option()
		choices[i].Selected = false
	}
	selected, err := chooseIndices(io, i18n.Text("engine.share.select_remove_skills"), choices,
		func(index int) string { return i18n.Format("engine.share.confirm_remove_one", listed[index].dirName) },
		i18n.Text("engine.share.no_skills_selected"))
	if err != nil {
		return nil, err
	}
	var skills []shareSkill
	for _, index := range selected {
		skills = append(skills, listed[index])
	}
	return skills, nil
}

// modSkillHasAnyAgent reports whether the entry names any of the targets. A
// nil entry never does, which is how an undeclared directory is rejected.
func modSkillHasAnyAgent(entry *modfile.ModSkill, targets []agents.Target) bool {
	if entry == nil {
		return false
	}
	for _, t := range targets {
		if hasAgent(entry.Agents, t.Name) {
			return true
		}
	}
	return false
}

// removeSkillAgents drops each named agent from the entry's list in every
// spelling, then drops the field itself when nothing is left.
func removeSkillAgents(entry *modfile.ModSkill, targets []agents.Target) {
	if entry == nil {
		return
	}
	entry.Agents = removeAgentNames(entry.Agents, targets)
}

// removeAgentNames drops only the named destinations, retaining historical
// ones in the lock until their links have also been taken down.
func removeAgentNames(names []string, targets []agents.Target) []string {
	drop := make(map[string]bool, len(targets))
	for _, t := range targets {
		drop[fsutil.FoldKey(t.Name)] = true
	}
	var remaining []string
	for _, name := range names {
		if !drop[fsutil.FoldKey(name)] {
			remaining = append(remaining, name)
		}
	}
	return remaining
}

// agentNames renders the targets for a diagnostic, and declaredAgentList
// renders what the entry does declare, so the error names both sides of the
// mismatch rather than only the half the caller got wrong.
func agentNames(targets []agents.Target) string {
	names := make([]string, len(targets))
	for i, t := range targets {
		names[i] = t.Name
	}
	return strings.Join(names, ", ")
}

func declaredAgentList(entry *modfile.ModSkill) string {
	if entry == nil {
		return i18n.Text("engine.share.not_declared_unknown")
	}
	if len(entry.Agents) == 0 {
		return i18n.Text("engine.share.not_declared_none")
	}
	return strings.Join(entry.Agents, ", ")
}

// classifyShareTarget inspects one destination without writing anything:
// install when it is absent, keep when it already links to (or matches) the
// managed copy, and conflict for anything else that would be discarded.
func (e *Engine) classifyShareTarget(sk shareSkill, dst string) (TargetStatus, string, error) {
	src := e.skillDir(sk.dirName)
	st, err := os.Lstat(dst)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// A missing destination is not a conflict; nothing to preserve.
			return ActionInstall, "", nil
		}
		return "", "", err
	}
	if st.Mode()&fs.ModeSymlink != 0 {
		if isDanglingManagedLink(dst, src) {
			// The stable link becomes readable once sync installs the source.
			return ActionKeep, i18n.Format("engine.share.linked_note", dst), nil
		}
		if resolved, linkErr := filepath.EvalSymlinks(dst); linkErr == nil {
			if managed, srcErr := filepath.EvalSymlinks(src); srcErr == nil &&
				fsutil.FoldKey(resolved) == fsutil.FoldKey(managed) {
				return ActionKeep, i18n.Format("engine.share.linked_note", dst), nil
			}
		}
		return ActionConflict, "", nil // A link to somewhere else would be discarded.
	}
	desired := sk.contentDir
	if desired == "" {
		desired = src
	}
	identical, err := shareIdentical(desired, dst)
	if err != nil {
		// An unhashable destination (stray file, permission trouble) holds
		// content skillmod cannot compare, so it must not be replaced silently.
		return ActionConflict, "", nil
	}
	if identical {
		return ActionKeep, i18n.Format("engine.share.identical_note", dst), nil
	}
	if desired != src {
		if clean, err := shareIdentical(src, dst); err == nil && clean {
			return ActionInstall, "", nil // A clean fallback copy follows the new version.
		}
	}
	return ActionConflict, "", nil
}

// linkShare places one destination entry through the same installation path
// get uses: a staged symlink to the managed copy (or a byte-preserving copy
// where the platform forbids links), with the previous destination preserved
// until the new one is in place.
func (e *Engine) linkShare(sk shareSkill, dst string) error {
	_, commit, err := e.stageShare(sk, dst)
	if err != nil {
		return err
	}
	commit()
	return nil
}

func (e *Engine) stageShare(sk shareSkill, dst string) (func() error, func(), error) {
	if err := rejectManagedOverlap(e.skillsDir(), filepath.Dir(dst)); err != nil {
		return nil, nil, err
	}
	return install.Link(e.skillDir(sk.dirName), dst)
}

// shareIdentical reports whether the destination already holds the same
// content as the installed skill.
func shareIdentical(src, dst string) (bool, error) {
	srcHash, err := dirhash.HashDir(src)
	if err != nil {
		return false, err
	}
	dstHash, err := dirhash.HashDir(dst)
	if err != nil {
		return false, err
	}
	return srcHash == dstHash, nil
}

// setShareTargetResult rewrites one target's action and note after conflict
// resolution or installation, matching how get edits its own results.
func setShareTargetResult(entry *EntryReport, path string, action TargetStatus, note string) {
	for i := range entry.TargetResults {
		if entry.TargetResults[i].Path == path {
			entry.TargetResults[i].Action = action
			entry.TargetResults[i].Note = note
			return
		}
	}
}

// shareableSkills lists the skills a share run can link: first-level
// directories under the scope's managed skills directory that contain a
// SKILL.md. The directory name, not the frontmatter name, is what appears at
// the destination. Each skill carries its declared agents; an undeclared slot
// instead carries existing mirror destinations so it can be adopted without
// making the caller repeat the previous selection.
func (e *Engine) shareableSkills(m *modfile.Mod, lock *modfile.Lock) ([]shareSkill, error) {
	base := e.skillsDir()
	dents, err := os.ReadDir(base)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]shareSkill, 0, len(dents))
	for _, d := range dents {
		dir := filepath.Join(base, d.Name())
		if st, statErr := os.Stat(dir); statErr != nil || !st.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
			continue
		}
		name, err := source.SkillNameFromDir(dir)
		if err != nil {
			// Use the directory name so an invalid SKILL.md can still be
			// shared as-is, mirroring init's placeholder rule.
			name = d.Name()
		}
		sk := shareSkill{dirName: d.Name(), name: name}
		if entry := findModSkill(m, d.Name()); entry != nil {
			sk.agents = append([]string(nil), entry.Agents...)
		} else {
			for _, target := range e.suggestedAgents() {
				dst := filepath.Join(target.Dir(e.Root), sk.dirName)
				action, _, err := e.classifyShareTarget(sk, dst)
				if err != nil {
					return nil, err
				}
				if action == ActionKeep {
					sk.agents = append(sk.agents, target.Name)
				}
			}
		}
		if lk := findLockByDir(lock, d.Name()); lk != nil {
			sk.version = lk.Version
		}
		out = append(out, sk)
	}
	return out, nil
}

// declares reports whether the entry already names the agent in any spelling,
// so the interactive selection can open on what the manifest says.
func (sk shareSkill) declares(name string) bool {
	return hasAgent(sk.agents, name)
}

// declaredByEvery reports whether all of the selected skills already name the
// agent. A destination opens checked only then: an agent that only some of the
// selection names would gain links on the skills that do not, which is an
// addition the caller never asked for. With every skill naming it, confirming
// the list unchanged is a no-op, and the checked boxes are the current state.
func declaredByEvery(skills []shareSkill, name string) bool {
	if len(skills) == 0 {
		return false
	}
	for _, sk := range skills {
		if !sk.declares(name) {
			return false
		}
	}
	return true
}

// selectShareSkills resolves the requested skills, falling back to the
// interactive selection when nothing was requested.
func selectShareSkills(listed []shareSkill, options ShareOptions, io IO) ([]shareSkill, error) {
	switch {
	case options.All:
		return listed, nil
	case len(options.Skills) > 0:
		return matchShareSkills(listed, options.Skills)
	}
	if len(listed) == 1 {
		return listed, nil
	}
	var targets []agents.Target
	for _, name := range options.Agents {
		target, err := agents.Resolve(name)
		if err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	optionsList := make([]ui.Option, len(listed))
	for i, sk := range listed {
		optionsList[i] = sk.option(targets...)
	}
	selected, err := chooseIndices(io,
		i18n.Text("engine.share.select_skills"),
		optionsList,
		func(index int) string { return i18n.Format("engine.share.confirm_one", listed[index].dirName) },
		i18n.Text("engine.share.no_skills_selected"))
	if err != nil {
		return nil, err
	}
	out := make([]shareSkill, 0, len(selected))
	for _, index := range selected {
		out = append(out, listed[index])
	}
	return out, nil
}

// chooseIndices resolves one multi-selection through the interaction share uses
// for both skills and destinations: a multi-select prompt where the terminal
// supports it, and one confirmation per item otherwise. confirmPrompt builds
// the per-item question for the fallback path, and emptyMessage is reported
// when nothing is selected. A missing confirmation channel is refused rather
// than read as "select nothing", so a non-interactive run fails loudly instead
// of silently sharing nothing.
func chooseIndices(io IO, prompt string, options []ui.Option, confirmPrompt func(int) string, emptyMessage string) ([]int, error) {
	if selector, ok := io.Confirm.(ui.MultiSelector); ok {
		selected, err := selector.ChooseMany(prompt, options)
		if err != nil {
			return nil, err
		}
		// A selector returning an out-of-range index cannot be trusted; treat it
		// like an empty selection rather than indexing past the list.
		seen := make(map[int]bool, len(selected))
		for _, index := range selected {
			if index < 0 || index >= len(options) || seen[index] {
				return nil, fmt.Errorf("%s", emptyMessage)
			}
			seen[index] = true
		}
		if len(selected) == 0 {
			return nil, fmt.Errorf("%s", emptyMessage)
		}
		return selected, nil
	}
	if io.Confirm == nil {
		return nil, fmt.Errorf("%s", i18n.Text("engine.share.non_interactive_selection"))
	}
	var selected []int
	for index := range options {
		confirmed, err := io.Confirm.Confirm(confirmPrompt(index))
		if err != nil {
			return nil, err
		}
		if confirmed {
			selected = append(selected, index)
		}
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("%s", emptyMessage)
	}
	return selected, nil
}

// matchShareSkills resolves explicit selectors against dir names and
// frontmatter names, case-insensitively like the other selection commands.
func matchShareSkills(listed []shareSkill, requested []string) ([]shareSkill, error) {
	selected := make([]shareSkill, 0, len(requested))
	seen := map[string]bool{}
	for _, token := range requested {
		matches := make([]shareSkill, 0, 2)
		for _, sk := range listed {
			if fsutil.FoldKey(sk.dirName) == fsutil.FoldKey(token) || fsutil.FoldKey(sk.name) == fsutil.FoldKey(token) {
				matches = append(matches, sk)
			}
		}
		switch len(matches) {
		case 0:
			names := make([]string, len(listed))
			for i, sk := range listed {
				names[i] = sk.dirName
			}
			return nil, fmt.Errorf(i18n.Text("engine.share.skill_not_found"), token, strings.Join(names, ", "))
		case 1:
		default:
			dirNames := make([]string, len(matches))
			for i, sk := range matches {
				dirNames[i] = sk.dirName
			}
			return nil, fmt.Errorf(i18n.Text("engine.share.skill_matches_multiple"), token, strings.Join(dirNames, ", "))
		}
		if !seen[fsutil.FoldKey(matches[0].dirName)] {
			seen[fsutil.FoldKey(matches[0].dirName)] = true
			selected = append(selected, matches[0])
		}
	}
	return selected, nil
}

// selectShareTargets resolves the destinations a run links into, and the agent
// names to record on each selected skill's entry. --agent names them, and the
// interactive selection offers the suggested agents; either way a destination
// is an agent name, and the directory it links through is derived from that
// name by the ".<name>/skills" rule. That derivation is the whole reason a
// declaration can be recorded: sync recreates the link from the name alone, on
// any machine, which a stored path could not do. The selection opens on the
// agents the chosen skills already declare, so a rerun that only adds a skill
// does not have to re-check what is already in place.
func (e *Engine) selectShareTargets(options ShareOptions, skills []shareSkill, io IO) (shareDeclaration, error) {
	var decl shareDeclaration
	seen := map[string]bool{}
	add := func(t agents.Target) {
		path := t.Dir(e.Root)
		if seen[fsutil.FoldKey(path)] {
			return
		}
		seen[fsutil.FoldKey(path)] = true
		decl.targets = append(decl.targets, shareTarget{label: t.Name, path: path})
		decl.declared = append(decl.declared, t.Name)
	}
	for _, name := range options.Agents {
		// Resolved and checked before this point; resolving again keeps the
		// declaration and the destination derived from the same source.
		t, err := agents.Resolve(name)
		if err != nil {
			return shareDeclaration{}, err
		}
		add(t)
	}
	if len(decl.targets) > 0 {
		return decl, nil
	}
	if io.Confirm == nil {
		// A run without a channel to choose through cannot invent targets.
		return shareDeclaration{}, fmt.Errorf("%s", i18n.Text("engine.share.no_targets"))
	}
	candidates := e.suggestedAgents(skills...)
	optionsList := make([]ui.Option, len(candidates))
	for i, t := range candidates {
		optionsList[i] = ui.Option{
			Label:       t.Name,
			Description: t.Dir(e.Root),
			Selected:    declaredByEvery(skills, t.Name),
		}
	}
	selected, err := chooseIndices(io,
		i18n.Text("engine.share.select_targets"),
		optionsList,
		func(index int) string {
			return i18n.Format("engine.share.confirm_target", candidates[index].Name, candidates[index].Dir(e.Root))
		},
		i18n.Text("engine.share.no_targets"))
	if err != nil {
		return shareDeclaration{}, err
	}
	for _, index := range selected {
		add(candidates[index])
	}
	return decl, nil
}

// suggestedAgents lists the destinations an interactive selection offers,
// minus the managed skills directory. That directory follows the same
// ".<name>/skills" shape as an agent, so it would otherwise be offered and
// then refused for overlapping what skillmod owns.
func (e *Engine) suggestedAgents(skills ...shareSkill) []agents.Target {
	managed := e.skillsDir()
	out := make([]agents.Target, 0, len(agents.Known()))
	seen := make(map[string]bool)
	for _, target := range agents.Suggested(e.Root) {
		if rejectManagedOverlap(managed, target.Dir(e.Root)) == nil {
			out = append(out, target)
			seen[target.Name] = true
		}
	}
	for _, sk := range skills {
		for _, name := range sk.agents {
			target, err := agents.Resolve(name)
			if err == nil && !seen[target.Name] && rejectManagedOverlap(managed, target.Dir(e.Root)) == nil {
				out = append(out, target)
				seen[target.Name] = true
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// shareTargetsFor resolves one skill's declaration into destination
// directories. A skill that declares no agents links nowhere, which is the
// normal state for a skill that stays only in the managed directory. An
// invalid agent name in the manifest is reported with the rule it breaks
// rather than skipped, so a hand-edited manifest fails loudly.
func (e *Engine) shareTargetsFor(m *modfile.Mod, dirName string) ([]shareTarget, error) {
	entry := findModSkill(m, dirName)
	if entry == nil || len(entry.Agents) == 0 {
		return nil, nil
	}
	out := make([]shareTarget, 0, len(entry.Agents))
	for _, name := range entry.Agents {
		t, err := agents.Resolve(name)
		if err != nil {
			return nil, err
		}
		if err := rejectManagedOverlap(e.skillsDir(), t.Dir(e.Root)); err != nil {
			return nil, err
		}
		out = append(out, shareTarget{label: t.Name, path: t.Dir(e.Root)})
	}
	return out, nil
}

// shareLinksToClean returns the recorded share destinations that mirror the
// managed copy of the named skill, so removing the skill takes its links down
// with it. The record is the lock's, not the declaration's: dropping a
// manifest entry removes the intent to share while the links it created stay
// on disk, and prune must still find them. A destination holding foreign
// content is left alone: remove must not discard a user's own directory that
// happens to share a name. When the managed copy itself is already gone, a
// link to it dangles and classify reports a conflict; the link is still
// skillmod's own and goes with the copy it names, while foreign links and
// fallback copies with real content stay.
func (e *Engine) shareLinksToClean(l *modfile.Lock, dirName, name string) ([]string, error) {
	recorded := recordedAgents(l, dirName)
	if len(recorded) == 0 {
		return nil, nil
	}
	sk := shareSkill{dirName: dirName, name: name}
	managed := e.skillDir(dirName)
	var out []string
	for _, agentName := range recorded {
		t, err := agents.Resolve(agentName)
		if err != nil {
			return nil, err
		}
		if err := rejectManagedOverlap(e.skillsDir(), t.Dir(e.Root)); err != nil {
			return nil, err
		}
		dst := filepath.Join(t.Dir(e.Root), dirName)
		action, _, err := e.classifyShareTarget(sk, dst)
		if err != nil {
			return nil, err
		}
		switch {
		case action == ActionKeep:
			out = append(out, dst)
		case action == ActionConflict && isDanglingManagedLink(dst, managed):
			out = append(out, dst)
		}
	}
	return out, nil
}

// recordedAgents returns the agents the lock records for one installation
// directory, or nil when the lock has no such record.
func recordedAgents(l *modfile.Lock, dirName string) []string {
	for _, lk := range l.Skills {
		if fsutil.FoldKey(lk.InstallDir()) == fsutil.FoldKey(dirName) {
			return lk.Agents
		}
	}
	return nil
}

// isDanglingManagedLink reports whether dst is a symlink whose recorded target
// is the managed directory. Readlink sees the link as it was written, without
// resolving, so it still answers when the managed copy no longer exists and
// EvalSymlinks fails.
func isDanglingManagedLink(dst, managed string) bool {
	target, err := os.Readlink(dst)
	if err != nil {
		return false // Not a symlink: foreign content stays untouched.
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(dst), target)
	}
	return fsutil.FoldKey(filepath.Clean(target)) == fsutil.FoldKey(filepath.Clean(managed))
}

// applyShareRemovals deletes share destinations that were taken down with a
// removed skill. Unlike managed removals it does not stage a backup: a share
// link is a convenience that sync recreates, and a leftover broken link is
// still something prune reports.
func (e *Engine) applyShareRemovals(paths []string) error {
	var errs []error
	for _, path := range paths {
		if err := rejectManagedOverlap(e.skillsDir(), filepath.Dir(path)); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := os.RemoveAll(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// reportShareRemovals lists the share links that leave with the managed
// copies, so remove and prune name them in the confirmation and the dry-run
// listing before anything is deleted. The header is already translated, so
// each command passes its own wording.
func reportShareRemovals(io IO, links []string, header string) error {
	if len(links) == 0 {
		return nil
	}
	if err := io.printf(header); err != nil {
		return err
	}
	for _, link := range links {
		if err := io.printf("  %s", link); err != nil {
			return err
		}
	}
	return nil
}

// plannedShareLink is one destination to (re)link when a declaration is
// aligned; the source is the managed directory of the named skill.
type plannedShareLink struct {
	skill shareSkill
	dst   string
}

// shareConflict is a share destination holding foreign content; dirName is
// the installation directory the destination entry was named after, which an
// alias may differ from the skill's frontmatter name.
type shareConflict struct {
	name    string
	dirName string
	dir     string
}

func (c shareConflict) asConflict() conflict {
	return conflict{name: c.name, dir: c.dir}
}

// classifyShareLinks walks every declared skill against that skill's declared
// share targets and classifies the destinations, so sync can settle conflicts
// together with its own before anything is linked. Actions are recorded on
// the report entry whose Directory names the installation directory; a
// directory no entry describes is not declared in the manifest, and sync
// reports it as stale for prune to handle, so it is not shared.
func (e *Engine) classifyShareLinks(m *modfile.Mod, rep *Report, entries []alignEntry) ([]plannedShareLink, []shareConflict, error) {
	contentByDir := make(map[string]string, len(entries))
	for _, entry := range entries {
		contentByDir[fsutil.FoldKey(entry.skill.DirName())] = entry.contentDir
	}
	var plans []plannedShareLink
	var conflicts []shareConflict
	for _, declaration := range m.Skills {
		sk := shareSkill{dirName: declaration.DirName(), name: declaration.Name,
			contentDir: contentByDir[fsutil.FoldKey(declaration.DirName())]}
		if declaration.Local {
			if _, err := os.Stat(filepath.Join(e.skillDir(sk.dirName), "SKILL.md")); err != nil {
				continue // Local content cannot be restored from a remote snapshot.
			}
		}
		entry := findEntryByDirectory(rep, sk.dirName)
		if entry == nil {
			continue
		}
		targets, err := e.shareTargetsFor(m, sk.dirName)
		if err != nil {
			return nil, nil, err
		}
		for _, target := range targets {
			dst := filepath.Join(target.path, sk.dirName)
			action, note, err := e.classifyShareTarget(sk, dst)
			if err != nil {
				return nil, nil, err
			}
			entry.TargetResults = append(entry.TargetResults, TargetReport{Path: dst, Action: action, Note: note})
			switch action {
			case ActionInstall:
				plans = append(plans, plannedShareLink{skill: sk, dst: dst})
			case ActionConflict:
				conflicts = append(conflicts, shareConflict{name: sk.name, dirName: sk.dirName, dir: dst})
			}
		}
	}
	return plans, conflicts, nil
}

// applyShareLinks links the classified destinations. It runs after the
// managed installations are final, because every link points at them.
func (e *Engine) applyShareLinks(plans []plannedShareLink, rep *Report) error {
	for _, plan := range plans {
		if err := e.linkShare(plan.skill, plan.dst); err != nil {
			return err
		}
		setShareTargetResult(findEntryByDirectory(rep, plan.skill.dirName), plan.dst, ActionInstalled, "")
	}
	return nil
}

// findEntryByDirectory returns the report entry whose directory matches, or
// nil when no report entry describes the installation directory.
func findEntryByDirectory(rep *Report, dirName string) *EntryReport {
	for i := range rep.Entries {
		if rep.Entries[i].Directory != "" && fsutil.FoldKey(rep.Entries[i].Directory) == fsutil.FoldKey(dirName) {
			return &rep.Entries[i]
		}
	}
	return nil
}

// rejectManagedOverlap refuses a destination that contains, is contained in,
// or equals the managed skills directory, so a share can never shadow or
// swallow the directory skillmod verifies.
func rejectManagedOverlap(skillsDir, dir string) error {
	managed, err := resolveSharePath(skillsDir)
	if err != nil {
		return err
	}
	destination, err := resolveSharePath(dir)
	if err != nil {
		return err
	}
	for _, pair := range [][2]string{{managed, destination}, {destination, managed}} {
		rel, err := filepath.Rel(pair[0], pair[1])
		if err != nil {
			continue
		}
		if rel == "." || filepath.IsLocal(rel) {
			return fmt.Errorf(i18n.Text("engine.share.overlap_managed"), dir, skillsDir)
		}
	}
	return nil
}

// resolveSharePath resolves existing ancestors too, so a destination that does
// not exist yet cannot hide an overlap behind a parent directory symlink.
func resolveSharePath(path string) (string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	parent := filepath.Dir(path)
	if parent == path {
		return "", err
	}
	resolved, err = resolveSharePath(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, filepath.Base(path)), nil
}

// option renders one skill for the interactive selection. With explicit
// targets, it opens checked only when already shared to every target, so
// accepting the defaults does not extend the links. Without targets, it
// opens on whether the skill is shared anywhere.
func (sk shareSkill) option(targets ...agents.Target) ui.Option {
	description := sk.version
	if description == "" {
		description = i18n.Text("engine.share.local_description")
	}
	selected := len(sk.agents) > 0
	for _, target := range targets {
		if !sk.declares(target.Name) {
			selected = false
			break
		}
	}
	return ui.Option{Label: sk.dirName, Description: description, Selected: selected}
}
