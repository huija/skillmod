// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"fmt"

	"github.com/huija/skillmod/internal/address"
	"github.com/huija/skillmod/internal/dirhash"
	"github.com/huija/skillmod/internal/modfile"
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
				fmt.Sprintf("冲突: %s 已存在且内容与锁定不符（本地可能被修改过）", c.dir),
				[]string{"覆盖", "保留并跳过", "中止"})
			switch choice {
			case 0: // Overwrite.
			case 1:
				skip[c.dir] = true
			default:
				return nil, fmt.Errorf("用户中止")
			}
		}
		return skip, nil
	}
	if io.Yes {
		for _, c := range conflicts {
			skip[c.dir] = true
			io.printf("冲突（--yes 自动保留并跳过）: %s", c.dir)
		}
		return skip, nil
	}
	msg := "检测到冲突（目标已存在且内容与锁定不符）："
	for _, c := range conflicts {
		msg += "\n  " + c.dir
	}
	return nil, fmt.Errorf("%s\n建议人工确认后重试（交互模式逐条选择），或 --yes 自动保留并跳过", msg)
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

	mat, err := e.resolveAndFetch(ctx, addr.Repo, addr.Subdir, addr.Ref, refsMemo{})
	if err != nil {
		return nil, err
	}
	name, err := source.SkillNameFromDir(mat.contentDir)
	if err != nil {
		return nil, err
	}
	if alias != "" && !validDirName(alias) {
		return nil, fmt.Errorf("别名 %q 含非法字符（仅允许字母数字 . _ -）", alias)
	}
	dir := name
	if alias != "" {
		dir = alias
	}
	srcStr := addr.Repo + subdirSuffix(addr.Subdir)

	// Reject a local-name conflict where the same directory name refers to different sources (AC-8).
	for _, sk := range m.Skills {
		if sk.DirName() == dir && !sameRemoteSource(sk.Source, srcStr) {
			return nil, &NameConflictError{Name: sk.Name, Existing: sk.Source, Incoming: srcStr}
		}
	}

	// Classify targets: install absent or clean old versions, skip matching versions, and flag local modifications as conflicts.
	adapters, err := e.adapters()
	if err != nil {
		return nil, err
	}
	prevHash := ""
	if old := findLock(lock, name); old != nil {
		prevHash = old.Dirhash
	}
	var targets []string
	var conflicts []conflict
	for _, a := range adapters {
		dst := adapterDir(a, e.Root, dir)
		switch classifyTarget(dst, mat.dirhash, prevHash) {
		case "install":
			targets = append(targets, dst)
		case "conflict":
			conflicts = append(conflicts, conflict{name: dir, dir: dst})
		}
	}
	skip, err := resolveConflicts(io, conflicts)
	if err != nil {
		return nil, err
	}
	for _, c := range conflicts {
		if !skip[c.dir] {
			targets = append(targets, c.dir) // Overwrite was selected.
		}
	}

	rep := &Report{Action: "get"}
	entry := EntryReport{Name: name, Source: srcStr, Version: mat.version, Action: "install", Targets: targets}
	entry.Note = mat.note
	rep.Entries = append(rep.Entries, entry)

	if io.DryRun {
		rep.Notes = append(rep.Notes, "dry-run：未写任何文件")
		return rep, nil
	}

	// Phase 2 installs first and writes mod and lock only on success; restore old directories if writing fails.
	finalize, err := applyInstalls([]plannedInstall{{name: dir, contentDir: mat.contentDir, targets: targets}})
	if err != nil {
		return nil, err
	}
	upsertMod(m, modfile.ModSkill{
		Name: name, Source: srcStr, Version: mat.version, Alias: alias,
	})
	upsertLock(lock, modfile.LockSkill{
		Name: name, Source: srcStr, Version: mat.version, Commit: mat.commit, Dirhash: mat.dirhash,
	})
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
	io.printf("已安装 %s %s，SKILL.mod 与 SKILL.lock 已更新", name, mat.version)
	return rep, nil
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
