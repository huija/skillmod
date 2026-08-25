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

// Verify checks every installation against SKILL.lock (PRD §3.4).
// It is read-only and never modifies files; detected drift returns DriftError, mapped to exit code 2 for AC-12.
func (e *Engine) Verify(ctx context.Context, io IO) (*Report, error) {
	m, err := e.loadMod()
	if err != nil {
		return nil, err
	}
	lock, err := loadLockStrict(e.Root)
	if err != nil {
		return nil, err
	}
	adapters, err := e.adapters()
	if err != nil {
		return nil, err
	}

	rep := &Report{Action: "verify"}
	drift := false
	for _, sk := range m.Skills {
		lk := findLock(lock, sk.Name)
		if lk == nil {
			rep.Entries = append(rep.Entries, EntryReport{Name: sk.Name, Source: sk.Source, Action: "drift", Note: "SKILL.lock 中无记录，建议执行 skillmod sync"})
			drift = true
			continue
		}
		for _, a := range adapters {
			dst := adapterDir(a, e.Root, sk.DirName())
			h, err := dirhash.HashDir(dst)
			switch {
			case err != nil:
				rep.Entries = append(rep.Entries, EntryReport{Name: sk.Name, Action: "drift", Note: "缺失: " + dst, Targets: []string{dst}})
				drift = true
			case h != lk.Dirhash:
				kind := "内容与锁定不符"
				if sk.Local {
					kind = "local 条目内容与基线不符（本地改动）"
				}
				rep.Entries = append(rep.Entries, EntryReport{Name: sk.Name, Action: "drift", Note: kind, Targets: []string{dst}})
				drift = true
			default:
				rep.Entries = append(rep.Entries, EntryReport{Name: sk.Name, Action: "ok", Version: lk.Version, Targets: []string{dst}})
			}
		}
	}
	// Report stale entries without treating them as drift.
	for _, lk := range staleEntries(m, lock) {
		rep.Entries = append(rep.Entries, EntryReport{
			Name: lk.Name, Action: "stale", Note: "已从 mod 移除，建议 skillmod prune 清理"})
	}

	if drift {
		io.printf("校验结论：有漂移")
		return rep, &DriftError{Report: rep}
	}
	io.printf("校验结论：全部一致")
	return rep, nil
}

// loadLockStrict treats a missing lock as an error under verify semantics (PRD §3.4 error table).
func loadLockStrict(root string) (*modfile.Lock, error) {
	l, err := modfile.LoadLock(root)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("未找到 SKILL.lock\n建议先执行 skillmod sync 生成锁定文件")
	}
	return l, err
}
