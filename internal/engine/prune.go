// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"fmt"
	"os"

	"github.com/huija/skillmod/internal/dirhash"
	"github.com/huija/skillmod/internal/modfile"
)

// Prune implements skillmod prune by cleaning installed files for stale entries present in the lock but absent from the mod.
// It lists and confirms changes first; locally modified files are kept while only their lock records are removed (PRD §3.6).
func (e *Engine) Prune(ctx context.Context, io IO) (*Report, error) {
	m, err := e.loadMod()
	if err != nil {
		return nil, err
	}
	lock := e.loadLock()
	stale := staleEntries(m, lock)
	rep := &Report{Action: "prune"}
	if len(stale) == 0 {
		rep.Notes = append(rep.Notes, "无过期条目")
		io.printf("无过期条目")
		return rep, nil
	}

	adapters, err := e.adapters()
	if err != nil {
		return nil, err
	}
	var deletable []string
	newLock := &modfile.Lock{}
	staleNames := map[string]bool{}
	for _, lk := range stale {
		staleNames[lk.Name] = true
	}
	for _, lk := range lock.Skills {
		if !staleNames[lk.Name] {
			newLock.Skills = append(newLock.Skills, lk) // Keep entries that are not stale.
		}
	}
	for _, lk := range stale {
		entry := EntryReport{Name: lk.Name, Source: lk.Source, Version: lk.Version}
		for _, a := range adapters {
			dst := adapterDir(a, e.Root, lk.Name)
			h, err := dirhash.HashDir(dst)
			if err != nil {
				continue // Nothing to clean when the target does not exist.
			}
			if h == lk.Dirhash {
				deletable = append(deletable, dst)
				entry.Targets = append(entry.Targets, dst)
			} else {
				entry.Note = "本地被修改过，保留文件、仅清 lock 记录: " + dst
			}
		}
		entry.Action = "prune"
		rep.Entries = append(rep.Entries, entry)
	}

	if len(deletable) > 0 {
		io.printf("将删除以下目录：")
		for _, d := range deletable {
			io.printf("  %s", d)
		}
		ok := io.Yes
		if !ok && io.Confirm != nil {
			ok = io.Confirm.Confirm(fmt.Sprintf("确认删除以上 %d 个目录？", len(deletable)))
		}
		if !ok && io.Confirm == nil && !io.Yes {
			return nil, fmt.Errorf("需要确认删除清单：交互模式重试，或 --yes 跳过确认；--dry-run 只列清单")
		}
		if !ok {
			return nil, fmt.Errorf("用户取消，未删除任何文件")
		}
	}

	if io.DryRun {
		rep.Notes = append(rep.Notes, "dry-run：未删除任何文件")
		return rep, nil
	}

	for _, d := range deletable {
		if err := os.RemoveAll(d); err != nil {
			return nil, fmt.Errorf("删除 %s: %w", d, err)
		}
	}
	if err := modfile.SaveLock(e.Root, newLock); err != nil {
		return nil, err
	}
	io.printf("已清理 %d 个过期条目", len(stale))
	return rep, nil
}
