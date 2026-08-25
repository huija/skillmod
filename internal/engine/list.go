// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"time"

	"github.com/huija/skillmod/internal/dirhash"
	"github.com/huija/skillmod/internal/resolve"
)

// List implements skillmod list by reporting every declared entry as installed, missing, drifted, or upgradable.
// It is read-only. Upgrade detection calls ls-remote once per unique repository and skips failures.
func (e *Engine) List(ctx context.Context, io IO) (*Report, error) {
	m, err := e.loadMod()
	if err != nil {
		return nil, err
	}
	lock := e.loadLock()
	adapters, err := e.adapters()
	if err != nil {
		return nil, err
	}

	// Call ls-remote once per unique repository on a best-effort basis.
	type latestKey struct{ repo, subdir string }
	latestCache := map[latestKey]string{}
	memo := refsMemo{}

	rep := &Report{Action: "list"}
	for _, sk := range m.Skills {
		entry := EntryReport{Name: sk.Name, Source: sk.Source, Version: sk.Version}
		if sk.Local {
			entry.Action = "local"
			rep.Entries = append(rep.Entries, entry)
			continue
		}
		// Installation status.
		status := "已安装"
		lk := findLock(lock, sk.Name)
		if lk == nil {
			status = "未锁定"
		} else {
			for _, a := range adapters {
				dst := adapterDir(a, e.Root, sk.DirName())
				h, err := dirhash.HashDir(dst)
				if err != nil {
					status = "缺失"
					break
				}
				if h != lk.Dirhash {
					status = "漂移"
					break
				}
			}
		}
		entry.Action = status

		// Upgrade detection; defer pseudo-version comparisons to update.
		repo, subdir, err := splitSource(sk.Source)
		if err == nil && lk != nil && !resolve.IsPseudoVersion(sk.Version) {
			key := latestKey{repo, subdir}
			latest, done := latestCache[key]
			if !done {
				rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
				refs, err := e.refs(rctx, repo, memo)
				cancel()
				if err == nil {
					if r, err := resolve.Resolve(resolve.Request{Repo: repo, Subdir: subdir}, refs); err == nil && r.Kind == resolve.KindTag {
						latest = r.Version
					}
				}
				latestCache[key] = latest
			}
			if latest != "" && latest != sk.Version {
				entry.Note = "可升级 → " + latest
			}
		}
		rep.Entries = append(rep.Entries, entry)
	}

	for _, en := range rep.Entries {
		note := ""
		if en.Note != "" {
			note = "（" + en.Note + "）"
		}
		io.printf("%-24s %-28s %s%s", en.Name, en.Version, en.Action, note)
	}
	return rep, nil
}
