// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/huija/skillmod/internal/fsutil"
	"github.com/huija/skillmod/internal/i18n"
	"github.com/huija/skillmod/internal/modfile"
	repoaddr "github.com/huija/skillmod/internal/repo"
	"github.com/huija/skillmod/internal/resolve"
	"github.com/huija/skillmod/internal/store"
)

const maxConcurrentRefQueries = 4

// UpdateOptions controls version selection behavior.
type UpdateOptions struct {
	AllowDowngrade bool
	DryRun         bool
}

// Update implements skillmod update [names...]: resolve the latest versions, update the lock, and install.
// A selector may be either the published skill name or its installation alias;
// a published name selects every declaration with that name. With no selectors
// it updates all remote entries. Commit-pinned entries, including pseudo-versions,
// advance to a new pseudo-version at default-branch HEAD.
func (e *Engine) Update(ctx context.Context, names []string, options UpdateOptions, io IO) (*Report, error) {
	defer io.stopProgress()
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

	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	var targets []modfile.ModSkill
	var localSelections []modfile.ModSkill
	for _, sk := range m.Skills {
		selected := len(want) == 0 || want[sk.Name] || want[sk.DirName()]
		if !selected {
			continue
		}
		if sk.Local {
			if len(want) > 0 {
				localSelections = append(localSelections, sk)
			}
			continue
		}
		targets = append(targets, sk)
	}
	for n := range want {
		found := false
		for _, sk := range m.Skills {
			if sk.Name == n || sk.DirName() == n {
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf(i18n.Text("engine.remove.entry_skill_mod"), n)
		}
	}

	rep := &Report{Action: CommandUpdate}
	for _, sk := range localSelections {
		rep.Entries = append(rep.Entries, EntryReport{
			Name: sk.Name, Local: true, Action: ActionSkip,
			Note: i18n.Text("engine.update.local_entry_skipped"),
		})
	}
	if len(targets) == 0 {
		if len(want) == 0 {
			rep.Notes = append(rep.Notes, i18n.Text("engine.update.no_remote_entries"))
		}
		return rep, printUpdateReport(rep, io)
	}
	var plans []plannedInstall
	var conflicts []conflict
	contentByDir := map[string]string{}
	reportByDir := map[string]int{}
	memo := newOperationMemo(io.Progress)
	repositories, err := updateRepositories(targets)
	if err != nil {
		return nil, err
	}
	if len(repositories) > 0 {
		io.setProgress(
			i18n.Format("engine.update.checking_source_repositories", len(repositories)),
			i18n.Text("engine.update.contacting_git_servers"),
			i18n.Text("engine.update.comparing_available_versions"),
		)
		if err := e.loadRefsConcurrently(ctx, repositories, memo); err != nil {
			return nil, fmt.Errorf(i18n.Text("engine.update.update_requires_network_access"), err)
		}
	}

	for _, sk := range targets {
		repo, subdir, err := splitSource(sk.Source)
		if err != nil {
			return nil, err
		}
		refs, err := e.refs(ctx, repo, memo)
		if err != nil {
			return nil, fmt.Errorf(i18n.Text("engine.update.update_requires_network_access"), err)
		}
		lk := findLock(lock, sk)
		cur := sk.Version
		installedVersion := cur
		if installedVersion == "" && lk != nil {
			installedVersion = lk.Version
		}

		var res resolve.Resolution
		if resolve.IsPseudoVersion(cur) || resolve.IsSHA(cur) {
			// Advance a commit-pinned entry to default-branch HEAD.
			if refs.DefaultHead == "" {
				return nil, fmt.Errorf(i18n.Text("engine.update.entry_remote_has_default"), sk.Name)
			}
			if lk != nil && refs.DefaultHead == lk.Commit {
				rep.Entries = append(rep.Entries, EntryReport{Name: sk.Name, Action: ActionKeep, Version: cur, Note: i18n.Text("engine.update.already_up_to_date")})
				continue
			}
			fetchRef := "HEAD"
			if refs.DefaultBranch != "" {
				fetchRef = "refs/heads/" + refs.DefaultBranch
			}
			res = resolve.Resolution{Kind: resolve.KindCommit, Commit: refs.DefaultHead, FetchRef: fetchRef}
		} else {
			r, err := resolve.Resolve(resolve.Request{Repo: repo, Subdir: subdir}, refs)
			if err != nil {
				return nil, err
			}
			if r.Kind == resolve.KindTag && resolve.CompareVersions(r.Version, installedVersion) < 0 && !options.AllowDowngrade {
				rep.Entries = append(rep.Entries, EntryReport{
					Name: sk.Name, Source: sk.Source, Action: ActionKeep, Version: installedVersion,
					Note: i18n.Format("engine.update.remote_latest_refusing", r.Version, installedVersion),
				})
				continue
			}
			if r.Version == installedVersion && lk != nil && lk.Commit != "" {
				if r.Commit == lk.Commit {
					rep.Entries = append(rep.Entries, EntryReport{Name: sk.Name, Action: ActionKeep, Version: installedVersion, Note: i18n.Text("engine.update.already_up_to_date")})
					continue
				}
				path, _ := e.Store.SnapshotPath(repo, installedVersion)
				return nil, &store.SnapshotConflictError{
					Path: path,
					Have: store.SnapshotInfo{Repo: repo, Version: installedVersion, Commit: lk.Commit},
					Want: store.SnapshotInfo{Repo: repo, Version: r.Version, Commit: r.Commit},
				}
			}
			res = *r
		}

		mat, err := e.materialize(ctx, repo, subdir, res, "", memo)
		if err != nil {
			return nil, err
		}
		contentByDir[sk.DirName()] = mat.contentDir
		// Conflict preflight: overwrite a clean old version matching the old lock hash; only local modifications conflict.
		prevHash := ""
		if lk != nil {
			prevHash = lk.Dirhash
		}
		var tgts []string
		var targetResults []TargetReport
		{
			dst := e.skillDir(sk.DirName())
			action := classifyTarget(dst, mat.dirhash, prevHash)
			targetResults = append(targetResults, TargetReport{Path: dst, Action: action})
			switch action {
			case ActionInstall:
				tgts = append(tgts, dst)
			case ActionConflict:
				conflicts = append(conflicts, conflict{name: sk.DirName(), dir: dst})
			}
		}
		if len(tgts) > 0 {
			plans = append(plans, plannedInstall{name: sk.DirName(), contentDir: mat.contentDir, targets: tgts})
		}
		rep.Entries = append(rep.Entries, EntryReport{
			Name: sk.Name, Source: sk.Source, Action: ActionUpdate,
			Version: mat.version, Note: fmt.Sprintf("%s → %s", installedVersion, mat.version), TargetResults: targetResults,
		})
		reportByDir[fsutil.FoldKey(sk.DirName())] = len(rep.Entries) - 1
		// Update the in-memory mod and lock; write them only after success.
		for i := range m.Skills {
			if sk.Version != "" && sameDir(m.Skills[i].DirName(), sk.DirName()) {
				m.Skills[i].Version = mat.version
				break
			}
		}
		upsertLock(lock, modfile.LockSkill{
			Name: sk.Name, Source: sk.Source, Version: mat.version, Commit: mat.commit, Dirhash: mat.dirhash, Dir: sk.Alias,
		})
	}

	io.stopProgress()
	skip, err := resolveConflicts(io, conflicts, ConflictAsk)
	if err != nil {
		return nil, err
	}
	for _, c := range conflicts {
		reportIndex, ok := reportByDir[fsutil.FoldKey(c.name)]
		if !ok {
			continue
		}
		if skip[c.dir] {
			rep.Entries[reportIndex].Action = ActionPartial
			setTargetResult(&rep.Entries[reportIndex], c.dir, ActionSkip)
			continue
		}
		setTargetResult(&rep.Entries[reportIndex], c.dir, ActionInstall)
		// For an overwrite, locate the corresponding entry's contentDir.
		for _, sk := range targets {
			if sk.DirName() != c.name {
				continue
			}
			if dir := contentByDir[c.name]; dir != "" {
				plans = append(plans, plannedInstall{name: c.name, contentDir: dir, targets: []string{c.dir}})
			}
		}
	}

	if options.DryRun {
		rep.Notes = append(rep.Notes, i18n.Text("engine.dry_run_files_written"))
		return rep, errors.Join(printUpdateReport(rep, io), partialError(rep, conflicts, skip))
	}

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
	return rep, errors.Join(printUpdateReport(rep, io), partialError(rep, conflicts, skip))
}

func printUpdateReport(rep *Report, io IO) error {
	for _, entry := range rep.Entries {
		switch entry.Action {
		case ActionKeep:
			if err := io.printf(i18n.Text("engine.update.entry_label"), entry.Name, entry.Version, entry.Note); err != nil {
				return err
			}
		case ActionUpdate, ActionSkip:
			if err := io.printf("%s: %s", entry.Name, entry.Note); err != nil {
				return err
			}
		}
	}
	return printReportNotes(rep, io)
}

func updateRepositories(targets []modfile.ModSkill) ([]string, error) {
	repositories := make([]string, 0, len(targets))
	for _, skill := range targets {
		repo, _, err := splitSource(skill.Source)
		if err != nil {
			return nil, err
		}
		repositories = append(repositories, repo)
	}
	return uniqueRepositories(repositories), nil
}

func (e *Engine) loadRefsConcurrently(ctx context.Context, repositories []string, memo *operationMemo) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make([]refsResult, len(repositories))
	limit := min(maxConcurrentRefQueries, len(repositories))
	semaphore := make(chan struct{}, limit)
	var wait sync.WaitGroup
	var errorOnce sync.Once
	var firstErr error

	for i, repo := range repositories {
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				return
			}

			refs, err := e.Source.Refs(ctx, repo)
			if err == nil {
				err = e.Store.PutRepoRefs(repo, refs)
			}
			results[i] = refsResult{refs: refs, err: err}
			if err != nil {
				errorOnce.Do(func() {
					firstErr = err
					cancel()
				})
			}
		}()
	}
	wait.Wait()
	if firstErr != nil {
		return firstErr
	}
	for i, repo := range repositories {
		memo.refs[repoaddr.Identity(repo)] = results[i]
	}
	return nil
}

func (e *Engine) loadRefsBestEffort(ctx context.Context, repositories []string, memo *operationMemo, timeout time.Duration) {
	repositories = uniqueRepositories(repositories)
	fetchCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	results := make([]refsResult, len(repositories))
	limit := min(maxConcurrentRefQueries, len(repositories))
	semaphore := make(chan struct{}, limit)
	var wait sync.WaitGroup
	for i, repo := range repositories {
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-fetchCtx.Done():
				results[i].err = fetchCtx.Err()
				return
			}
			refs, err := e.Source.Refs(fetchCtx, repo)
			if err == nil && e.Store != nil {
				err = e.Store.PutRepoRefs(repo, refs)
			}
			results[i] = refsResult{refs: refs, err: err}
		}()
	}
	wait.Wait()
	for i, repo := range repositories {
		memo.refs[repoaddr.Identity(repo)] = results[i]
	}
}

func uniqueRepositories(repositories []string) []string {
	seen := make(map[string]bool, len(repositories))
	unique := make([]string, 0, len(repositories))
	for _, repo := range repositories {
		identity := repoaddr.Identity(repo)
		if seen[identity] {
			continue
		}
		seen[identity] = true
		unique = append(unique, repo)
	}
	return unique
}
