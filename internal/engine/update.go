// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"fmt"
	"sync"

	"github.com/huija/skillmod/internal/i18n"
	"github.com/huija/skillmod/internal/modfile"
	"github.com/huija/skillmod/internal/resolve"
	"github.com/huija/skillmod/internal/source"
	"github.com/huija/skillmod/internal/store"
)

const maxConcurrentRefQueries = 4

// Update implements skillmod update [names...]: resolve the latest versions, update the lock, and install.
// With no names it updates all remote entries; commit-pinned entries, including pseudo-versions, advance to a new pseudo-version at default-branch HEAD (PRD §3.6).
func (e *Engine) Update(ctx context.Context, names []string, io IO) (*Report, error) {
	defer io.stopProgress()
	m, err := e.loadMod()
	if err != nil {
		return nil, err
	}
	lock := e.loadLock()

	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	var targets []modfile.ModSkill
	for _, sk := range m.Skills {
		if sk.Local || (len(want) > 0 && !want[sk.Name]) {
			continue
		}
		targets = append(targets, sk)
	}
	for n := range want {
		found := false
		for _, sk := range m.Skills {
			if sk.Name == n {
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf(i18n.Text("entry %q is not in SKILL.mod"), n)
		}
	}

	rep := &Report{Action: "update"}
	var plans []plannedInstall
	var conflicts []conflict
	contentByDir := map[string]string{}
	memo := newOperationMemo(io.Progress)
	adapters, err := e.adapters()
	if err != nil {
		return nil, err
	}
	repositories, err := updateRepositories(targets)
	if err != nil {
		return nil, err
	}
	if len(repositories) > 0 {
		io.setProgress(
			i18n.Format("checking %d source repositories concurrently", len(repositories)),
			i18n.Text("contacting Git servers"),
			i18n.Text("comparing available versions"),
		)
		if err := e.loadRefsConcurrently(ctx, repositories, memo); err != nil {
			return nil, fmt.Errorf(i18n.Text("update requires network access to resolve the latest version: %w"), err)
		}
	}

	for _, sk := range targets {
		repo, subdir, err := splitSource(sk.Source)
		if err != nil {
			return nil, err
		}
		refs, err := e.refs(ctx, repo, memo)
		if err != nil {
			return nil, fmt.Errorf(i18n.Text("update requires network access to resolve the latest version: %w"), err)
		}
		lk := findLock(lock, sk.Name)
		cur := sk.Version

		var res resolve.Resolution
		if resolve.IsPseudoVersion(cur) || resolve.IsSHA(cur) {
			// Advance a commit-pinned entry to default-branch HEAD (PRD §3.6).
			if refs.DefaultHead == "" {
				return nil, fmt.Errorf(i18n.Text("entry %s: remote has no default branch"), sk.Name)
			}
			if lk != nil && refs.DefaultHead == lk.Commit {
				rep.Entries = append(rep.Entries, EntryReport{Name: sk.Name, Action: "keep", Version: cur, Note: i18n.Text("already up to date")})
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
			if r.Version == cur && lk != nil && lk.Commit != "" {
				if r.Commit == lk.Commit {
					rep.Entries = append(rep.Entries, EntryReport{Name: sk.Name, Action: "keep", Version: cur, Note: i18n.Text("already up to date")})
					continue
				}
				path, _ := e.Store.SnapshotPath(repo, cur)
				return nil, &store.SnapshotConflictError{
					Path: path,
					Have: store.SnapshotInfo{Repo: repo, Version: cur, Commit: lk.Commit},
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
		for _, a := range adapters {
			dst := adapterDir(a, e.Root, sk.DirName())
			switch classifyTarget(dst, mat.dirhash, prevHash) {
			case "install":
				tgts = append(tgts, dst)
			case "conflict":
				conflicts = append(conflicts, conflict{name: sk.DirName(), dir: dst})
			}
		}
		if len(tgts) > 0 {
			plans = append(plans, plannedInstall{name: sk.DirName(), contentDir: mat.contentDir, targets: tgts})
		}
		rep.Entries = append(rep.Entries, EntryReport{
			Name: sk.Name, Source: sk.Source, Action: "update",
			Version: mat.version, Note: fmt.Sprintf("%s → %s", cur, mat.version),
		})
		// Update the in-memory mod and lock; write them only after success.
		for i := range m.Skills {
			if m.Skills[i].Name == sk.Name {
				m.Skills[i].Version = mat.version
			}
		}
		upsertLock(lock, modfile.LockSkill{
			Name: sk.Name, Source: sk.Source, Version: mat.version, Commit: mat.commit, Dirhash: mat.dirhash,
		})
	}

	io.stopProgress()
	skip, err := resolveConflicts(io, conflicts)
	if err != nil {
		return nil, err
	}
	for _, c := range conflicts {
		if skip[c.dir] {
			continue
		}
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

	if io.DryRun {
		rep.Notes = append(rep.Notes, i18n.Text("dry-run: no files were written"))
		return rep, nil
	}

	finalize, err := applyInstalls(plans)
	if err != nil {
		return nil, err
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
	for _, en := range rep.Entries {
		switch en.Action {
		case "keep":
			io.printf(i18n.Text("%s: %s (%s)"), en.Name, en.Version, en.Note)
		case "update":
			io.printf("%s: %s", en.Name, en.Note)
		}
	}
	return rep, nil
}

func updateRepositories(targets []modfile.ModSkill) ([]string, error) {
	seen := make(map[string]bool, len(targets))
	repositories := make([]string, 0, len(targets))
	for _, skill := range targets {
		repo, _, err := splitSource(skill.Source)
		if err != nil {
			return nil, err
		}
		identity := source.RepoIdentity(repo)
		if seen[identity] {
			continue
		}
		seen[identity] = true
		repositories = append(repositories, repo)
	}
	return repositories, nil
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
		memo.refs[source.RepoIdentity(repo)] = results[i]
	}
	return nil
}
