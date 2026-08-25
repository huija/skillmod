// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/huija/skillmod/internal/dirhash"
	"github.com/huija/skillmod/internal/install"
	"github.com/huija/skillmod/internal/modfile"
	"github.com/huija/skillmod/internal/resolve"
	"github.com/huija/skillmod/internal/source"
)

// Init implements skillmod init by scanning existing skills and drafting SKILL.mod (PRD §3.1).
// It only reads existing skill files, refuses to run when SKILL.mod exists, and backs up and rebuilds with --force.
func (e *Engine) Init(ctx context.Context, force bool, io IO) (*Report, error) {
	modPath := filepath.Join(e.Root, modfile.ModFileName)
	if _, err := os.Stat(modPath); err == nil {
		if !force {
			return nil, fmt.Errorf("已存在 %s\n建议：确认后再用 --force 重新生成（原文件备份为 SKILL.mod.bak）", modPath)
		}
		if err := copyFile(modPath, modPath+".bak"); err != nil {
			return nil, fmt.Errorf("备份失败: %w", err)
		}
	}

	// init is the migration and discovery entry point; scan all known platform directories independently of configured installation targets.
	adapters := install.All()

	// Scan first-level subdirectories in each platform's skill directory.
	type scanned struct {
		name    string // SKILL.md frontmatter name; use the directory name as a placeholder on parse failure
		dirName string
		srcDir  string
		note    string
	}
	seen := map[string]*scanned{}
	for _, a := range adapters {
		base := a.SkillsDir(e.Root)
		dents, err := os.ReadDir(base)
		if err != nil {
			continue // The platform directory does not exist.
		}
		for _, d := range dents {
			if !d.IsDir() {
				continue
			}
			dir := filepath.Join(base, d.Name())
			if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
				continue
			}
			s := &scanned{dirName: d.Name(), srcDir: dir}
			name, err := source.SkillNameFromDir(dir)
			if err != nil {
				s.name = d.Name() // Use the directory name as specified by the PRD §3.1 error table.
				s.note = "SKILL.md name 解析失败，以目录名占位，请人工修正"
			} else {
				s.name = name
			}
			if prev, ok := seen[s.name]; ok {
				prev.note = "同名 skill 出现在多个平台目录，已合并为一条"
				continue // Treat matching names as the same skill.
			}
			seen[s.name] = s
		}
	}

	// Match sources with one batched ls-remote per known source rather than one network request per skill.
	type srcRefs struct {
		repo string
		refs *resolve.Refs
	}
	var sources []srcRefs
	for _, repo := range e.Config.KnownSources {
		rctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		refs, err := e.Source.Refs(rctx, repo)
		cancel()
		if err != nil {
			io.printf("提示: 源 %s 匹配失败（%v），相关条目按 local 处理", repo, err)
			continue
		}
		sources = append(sources, srcRefs{repo, refs})
	}

	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)

	m := &modfile.Mod{SchemaVersion: modfile.SchemaVersion}
	lock := e.loadLock()
	rep := &Report{Action: "init"}
	for _, name := range names {
		s := seen[name]
		entry := EntryReport{Name: name}
		var matched *modfile.ModSkill
		for _, sr := range sources {
			// Monorepo convention: match a <directory-name>/v* tag prefix.
			if r, err := resolve.Resolve(resolve.Request{Repo: sr.repo, Subdir: s.dirName}, sr.refs); err == nil && r.Kind == resolve.KindTag && hasPrefixTag(sr.refs, s.dirName+"/") {
				matched = &modfile.ModSkill{Name: name, Source: sr.repo + "//" + s.dirName, Version: r.Version}
				break
			}
			// Single-repository convention: the repository name equals the directory name and has a root tag.
			if strings.TrimSuffix(path.Base(sr.repo), ".git") == s.dirName {
				if r, err := resolve.Resolve(resolve.Request{Repo: sr.repo}, sr.refs); err == nil && r.Kind == resolve.KindTag {
					matched = &modfile.ModSkill{Name: name, Source: sr.repo, Version: r.Version}
					break
				}
			}
		}
		if matched != nil {
			entry.Source = matched.Source
			entry.Version = matched.Version
			entry.Action = "matched"
			m.Skills = append(m.Skills, *matched)
		} else {
			// For a local entry, record its name and baseline content dirhash (PRD §3.1 rule 2).
			h, err := dirhash.HashDir(s.srcDir)
			if err != nil {
				return nil, err
			}
			m.Skills = append(m.Skills, modfile.ModSkill{Name: name, Local: true})
			upsertLock(lock, modfile.LockSkill{Name: name, Dirhash: h})
			entry.Action = "local"
		}
		if s.note != "" {
			entry.Note = s.note
		}
		rep.Entries = append(rep.Entries, entry)
	}

	if len(rep.Entries) == 0 {
		rep.Notes = append(rep.Notes, "未发现 skill，已生成空清单，可用 skillmod get 添加")
	}

	// Confirm each entry individually as required by the PRD interaction flow.
	if !io.Yes && io.Confirm == nil {
		rep.Notes = append(rep.Notes, "非交互环境未确认：重跑加 --yes 全部采纳")
		return rep, fmt.Errorf("init 需要确认：交互模式逐项选择，或 --yes 全部采纳")
	}
	if io.Confirm != nil && !io.Yes {
		var kept []modfile.ModSkill
		var keptEntries []EntryReport
		for i, sk := range m.Skills {
			if io.Confirm.Confirm(fmt.Sprintf("采纳条目 %s（%s）？", sk.Name, rep.Entries[i].Action)) {
				kept = append(kept, sk)
				keptEntries = append(keptEntries, rep.Entries[i])
			}
		}
		m.Skills = kept
		rep.Entries = keptEntries
		// Do not add rejected local entries to the lock.
		keepNames := map[string]bool{}
		for _, sk := range kept {
			keepNames[sk.Name] = true
		}
		var keptLock []modfile.LockSkill
		for _, lk := range lock.Skills {
			if keepNames[lk.Name] {
				keptLock = append(keptLock, lk)
			}
		}
		lock.Skills = keptLock
	}

	if io.DryRun {
		rep.Notes = append(rep.Notes, "dry-run：未写任何文件")
		return rep, nil
	}
	if err := modfile.SaveMod(e.Root, m); err != nil {
		return nil, err
	}
	if len(lock.Skills) > 0 {
		if err := modfile.SaveLock(e.Root, lock); err != nil {
			return nil, err
		}
	}
	io.printf("已生成 %s（%d 个条目），原文件零改动", modPath, len(m.Skills))
	io.printf("下一步：skillmod sync 对齐锁定状态，并将 SKILL.mod / SKILL.lock 提交进版本库")
	return rep, nil
}

func hasPrefixTag(refs *resolve.Refs, prefix string) bool {
	for tag := range refs.Tags {
		if len(tag) > len(prefix) && tag[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}
