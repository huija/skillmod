// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"time"

	"github.com/huija/skillmod/internal/i18n"
	repoaddr "github.com/huija/skillmod/internal/repo"
	"github.com/huija/skillmod/internal/resolve"
)

// refsCacheMaxAge is how long a cached ls-remote snapshot stays fresh for
// list's upgrade detection. list is a read-only inspection command: within
// this window it answers from the local cache, and skillmod update is the
// command that deliberately contacts the remote. It is a variable so tests can
// age a cache without waiting.
var refsCacheMaxAge = 15 * time.Minute

// List implements skillmod list by reporting every declared entry as installed, missing, drifted, or upgradable.
// It is read-only. Upgrade detection calls ls-remote once per unique repository whose cached reference
// snapshot has gone stale, and skips failures.
func (e *Engine) List(ctx context.Context, io IO) (*Report, error) {
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

	// Call ls-remote once per unique repository on a best-effort basis, and
	// only for repositories whose cached reference snapshot is older than the
	// freshness window: a warm cache makes repeated list runs local-only, and
	// update remains the command that deliberately refreshes.
	type latestKey struct{ repo, subdir string }
	latestCache := map[latestKey]string{}
	memo := newOperationMemo(nil)
	queried := map[string]bool{}
	var stale []string
	for _, skill := range m.Skills {
		if skill.Local || findLock(lock, skill) == nil || resolve.IsPseudoVersion(skill.Version) {
			continue
		}
		repo, _, err := splitSource(skill.Source)
		if err != nil {
			continue
		}
		identity := repoaddr.Identity(repo)
		if queried[identity] {
			continue
		}
		queried[identity] = true
		cached, ok, cacheErr := e.Store.GetRepoRefs(repo)
		if cacheErr == nil && ok && time.Since(cached.FetchedAt) < refsCacheMaxAge {
			memo.refs[identity] = refsResult{refs: cached.Refs}
			continue
		}
		stale = append(stale, repo) // Absent, unreadable, or aged out: refresh it.
	}
	e.loadRefsBestEffort(ctx, stale, memo, 10*time.Second)

	rep := &Report{Action: CommandList}
	for _, sk := range m.Skills {
		lk := findLock(lock, sk)
		entry := e.inspectSkill(sk, lk)

		// Upgrade detection; defer pseudo-version comparisons to update.
		repo, subdir, err := splitSource(sk.Source)
		if err == nil && lk != nil && !resolve.IsPseudoVersion(sk.Version) {
			key := latestKey{repo, subdir}
			latest, done := latestCache[key]
			if !done {
				refs, err := e.refs(ctx, repo, memo)
				if err == nil {
					if r, err := resolve.Resolve(resolve.Request{Repo: repo, Subdir: subdir}, refs); err == nil && r.Kind == resolve.KindTag {
						latest = r.Version
					}
				}
				latestCache[key] = latest
			}
			if latest != "" && resolve.CompareVersions(latest, lk.Version) > 0 {
				entry.Note = appendNote(entry.Note, i18n.Text("engine.list.upgrade_available")+latest)
			}
		}
		rep.Entries = append(rep.Entries, entry)
	}

	for _, en := range rep.Entries {
		note := ""
		if en.Note != "" {
			note = i18n.Format("engine.list.list", en.Note)
		}
		action := displayListAction(en.Action)
		if en.Local && en.Action == ActionInstalled {
			action = string(ActionLocal)
		}
		if err := io.printf("%-24s %-28s %s%s", en.Name, en.Version, action, note); err != nil {
			return rep, err
		}
	}
	return rep, nil
}

func displayListAction(action EntryStatus) string {
	switch action {
	case ActionInstalled:
		return i18n.Text("engine.list.installed")
	case ActionUnlocked:
		return i18n.Text("engine.list.unlocked")
	case ActionMissing:
		return i18n.Text("engine.list.missing")
	case ActionDrift:
		return i18n.Text("engine.list.drift")
	default:
		return string(action)
	}
}
