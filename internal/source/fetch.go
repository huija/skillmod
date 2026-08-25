// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package source

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// File is one file in a subtree, including Git blob bytes and its executable bit.
type File struct {
	Path string // slash-separated path relative to the subtree
	Exec bool   // git mode 100755
	Data []byte
}

// Tree is a complete repository snapshot at one commit. Skill repositories are typically only megabytes, so the full tree stays in memory.
type Tree struct {
	Commit     string
	CommitTime time.Time // UTC committer date used by pseudo-versions
	Files      []File
	Symlinks   []string // paths relative to the repository root; snapshots store link-target blob bytes
	Submodules []string // paths relative to the repository root; record boundaries because no blob can be materialized
}

// SymlinkError reports a skill containing a symlink, which v0.1 refuses to install (PRD §4).
type SymlinkError struct{ Path string }

func (e *SymlinkError) Error() string {
	return fmt.Sprintf("skill 包含 symlink（%s），v0.1 不支持含 symlink 的 skill", e.Path)
}

// SubmoduleError reports a Git submodule that cannot be preserved byte for byte.
type SubmoduleError struct{ Path string }

func (e *SubmoduleError) Error() string {
	return fmt.Sprintf("skill 包含 submodule（%s），v0.1 不支持", e.Path)
}

// NoSkillMDError reports a subtree with no SKILL.md or an unparseable frontmatter name.
type NoSkillMDError struct{ Detail string }

func (e *NoSkillMDError) Error() string {
	return "skill 包 SKILL.md 缺失或 frontmatter name 非法（联系作者修复）: " + e.Detail
}

// Fetch retrieves the complete repository tree at repo@commit, first attempting a targeted commit fetch
// when no known ref is available.
func (s *Source) Fetch(ctx context.Context, repo, commit string) (*Tree, error) {
	return s.FetchRef(ctx, repo, commit, "")
}

// FetchRef uses one persistent bare repository per remote, fetches one complete
// immutable repository revision, then reads its exact tree through Git objects.
// No checkout occurs, so CRLF conversion and worktree filters cannot change bytes.
func (s *Source) FetchRef(ctx context.Context, repo, commit, fetchRef string) (*Tree, error) {
	tmp, cleanup, err := s.openRepo(ctx, repo)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	if err := s.fetchTarget(ctx, tmp, commit, fetchRef); err != nil {
		return nil, err
	}

	// Committer date used as the time component of a pseudo-version.
	out, err := s.run(ctx, tmp, "log", "-1", "--format=%ct", commit)
	if err != nil {
		return nil, err
	}
	ct, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("解析 commit 时间失败: %q", out)
	}

	// List the complete repository; -z prevents core.quotePath from escaping non-ASCII paths.
	// On the first fetch of repo@version, obtain every blob so other skills at that version can be materialized locally.
	args := []string{"ls-tree", "-r", "-z", commit}
	out, err = s.run(ctx, tmp, args...)
	if err != nil {
		return nil, err
	}
	tree := &Tree{Commit: commit, CommitTime: time.Unix(ct, 0).UTC()}
	entries, err := parseLsTree(out, "")
	if err != nil {
		return nil, err
	}

	// A partial clone promises only blobs. Combine every missing object for this version into one fetch
	// so cat-file does not trigger the promisor remote per object and create many small packs.
	var blobEntries []lsEntry
	for _, entry := range entries {
		if entry.typ == "blob" {
			blobEntries = append(blobEntries, entry)
		}
	}
	prefetchErr := s.prefetchMissingBlobs(ctx, tmp, blobEntries)
	blobs, err := s.catFileBatch(ctx, tmp, blobEntries, prefetchErr == nil)
	if err != nil && prefetchErr == nil {
		// A server may report a successful batch fetch without returning every promised object.
		// Retain Git's native lazy fetching as a compatibility fallback.
		blobs, err = s.catFileBatch(ctx, tmp, blobEntries, false)
	}
	if err != nil {
		if prefetchErr != nil {
			return nil, fmt.Errorf("批量抓取 repo blob 失败（惰性抓取兜底也失败）: %v: %w", prefetchErr, err)
		}
		return nil, err
	}
	for _, e := range entries {
		if e.typ == "commit" || e.mode == "160000" {
			tree.Submodules = append(tree.Submodules, e.path)
			continue
		}
		if e.mode == "120000" {
			tree.Symlinks = append(tree.Symlinks, e.path)
		}
		tree.Files = append(tree.Files, File{Path: e.path, Exec: e.mode == "100755", Data: blobs[e.sha]})
	}
	return tree, nil
}

type lsEntry struct {
	mode, typ, sha, path string
}

// parseLsTree parses `git ls-tree -r -z` output in the form "<mode> <type> <sha>\t<path>\0".
// Removing prefix yields a path relative to the subtree.
func parseLsTree(out, prefix string) ([]lsEntry, error) {
	var entries []lsEntry
	for _, rec := range strings.Split(strings.TrimRight(out, "\x00"), "\x00") {
		if rec == "" {
			continue
		}
		tab := strings.IndexByte(rec, '\t')
		if tab < 0 {
			return nil, fmt.Errorf("ls-tree 输出畸形: %q", rec)
		}
		meta, path := rec[:tab], rec[tab+1:]
		parts := strings.Fields(meta)
		if len(parts) != 3 {
			return nil, fmt.Errorf("ls-tree 输出畸形: %q", rec)
		}
		mode, typ, sha := parts[0], parts[1], parts[2]
		if typ != "blob" && typ != "commit" {
			continue
		}
		if prefix != "" {
			p, ok := strings.CutPrefix(path, prefix+"/")
			if !ok {
				continue
			}
			path = p
		}
		entries = append(entries, lsEntry{mode: mode, typ: typ, sha: sha, path: path})
	}
	return entries, nil
}

func (s *Source) prefetchMissingBlobs(ctx context.Context, dir string, entries []lsEntry) error {
	missing, err := s.missingBlobs(ctx, dir, entries)
	if err != nil || len(missing) == 0 {
		return err
	}
	git := s.Git
	if git == "" {
		git = "git"
	}
	remote, err := s.run(ctx, dir, "remote", "get-url", "origin")
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, git,
		"-c", "protocol.version=2",
		"fetch-pack", "--no-progress", "--refetch", "--thin", "--stdin", strings.TrimSpace(remote),
	)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C", "GIT_NO_LAZY_FETCH=1")
	cmd.Stdin = strings.NewReader(strings.Join(missing, "\n") + "\n")
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git fetch-pack --stdin: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	remaining, err := s.missingBlobs(ctx, dir, entries)
	if err != nil {
		return err
	}
	if len(remaining) != 0 {
		return fmt.Errorf("批量 fetch 后仍缺少 %d 个 blob（stdout=%q stderr=%q）", len(remaining), strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()))
	}
	return nil
}

func (s *Source) missingBlobs(ctx context.Context, dir string, entries []lsEntry) ([]string, error) {
	seen := make(map[string]bool, len(entries))
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !seen[entry.sha] {
			seen[entry.sha] = true
			ids = append(ids, entry.sha)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	git := s.Git
	if git == "" {
		git = "git"
	}
	cmd := exec.CommandContext(ctx, git, "cat-file", "--batch-check")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C", "GIT_NO_LAZY_FETCH=1")
	cmd.Stdin = strings.NewReader(strings.Join(ids, "\n") + "\n")
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git cat-file --batch-check: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != len(ids) {
		return nil, fmt.Errorf("cat-file --batch-check 返回 %d 行，期望 %d 行", len(lines), len(ids))
	}
	var missing []string
	for i, line := range lines {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == ids[i] && fields[1] == "missing" {
			missing = append(missing, ids[i])
			continue
		}
		if len(fields) < 3 || fields[0] != ids[i] || fields[1] != "blob" {
			return nil, fmt.Errorf("cat-file --batch-check 输出畸形: %q", line)
		}
	}
	return missing, nil
}

// catFileBatch reads every blob byte for byte through one `git cat-file --batch` process.
func (s *Source) catFileBatch(ctx context.Context, dir string, entries []lsEntry, noLazyFetch bool) (map[string][]byte, error) {
	git := s.Git
	if git == "" {
		git = "git"
	}
	cmd := exec.CommandContext(ctx, git, "cat-file", "--batch")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	if noLazyFetch {
		cmd.Env = append(cmd.Env, "GIT_NO_LAZY_FETCH=1")
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动 git cat-file: %w", err)
	}
	go func() {
		for _, e := range entries {
			io.WriteString(stdin, e.sha+"\n")
		}
		stdin.Close()
	}()

	out := make(map[string][]byte, len(entries))
	r := bufio.NewReader(stdout)
	for _, e := range entries {
		header, err := r.ReadString('\n')
		if err != nil {
			cmd.Wait()
			return nil, fmt.Errorf("读 blob %s 头失败: %w", e.sha, err)
		}
		parts := strings.Fields(strings.TrimRight(header, "\n"))
		if len(parts) != 3 || parts[1] != "blob" {
			cmd.Wait()
			return nil, fmt.Errorf("blob %s 不可读: %s", e.sha, strings.TrimSpace(header))
		}
		size, err := strconv.Atoi(parts[2])
		if err != nil {
			cmd.Wait()
			return nil, err
		}
		data := make([]byte, size)
		if _, err := io.ReadFull(r, data); err != nil {
			cmd.Wait()
			return nil, fmt.Errorf("读 blob %s 内容失败: %w", e.sha, err)
		}
		if _, err := r.ReadByte(); err != nil { // Separator newline after the blob.
			cmd.Wait()
			return nil, err
		}
		out[e.sha] = data
	}
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("git cat-file: %w", err)
	}
	return out, nil
}

// SkillName reads the name field from SKILL.md frontmatter at the subtree root, the PRD's sole authority for local names.
func (t *Tree) SkillName() (string, error) {
	for _, f := range t.Files {
		if f.Path != "SKILL.md" {
			continue
		}
		name, err := ParseSkillName(string(f.Data))
		if err != nil {
			return "", err
		}
		return name, nil
	}
	return "", &NoSkillMDError{Detail: "子树根缺少 SKILL.md"}
}

// SkillNameFromDir reads the SKILL.md frontmatter name from a directory on disk for version-snapshot hits.
func SkillNameFromDir(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return "", &NoSkillMDError{Detail: "子树根缺少 SKILL.md"}
	}
	return ParseSkillName(string(data))
}

// ParseSkillName parses the name field from SKILL.md frontmatter using a minimal YAML subset:
// it extracts a scalar `name:` from a block delimited by `---`, which covers canonical frontmatter.
func ParseSkillName(content string) (string, error) {
	if !strings.HasPrefix(content, "---\n") {
		return "", &NoSkillMDError{Detail: "缺少 --- frontmatter 起始行"}
	}
	rest := content[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", &NoSkillMDError{Detail: "缺少 --- frontmatter 结束行"}
	}
	for _, line := range strings.Split(rest[:end], "\n") {
		v, ok := strings.CutPrefix(line, "name:")
		if !ok {
			continue
		}
		name := strings.Trim(strings.TrimSpace(v), `"'`)
		if name == "" {
			return "", &NoSkillMDError{Detail: "name 字段为空"}
		}
		if strings.ContainsAny(name, "/\\") || name == "." || name == ".." {
			return "", &NoSkillMDError{Detail: fmt.Sprintf("name %q 含非法字符", name)}
		}
		return name, nil
	}
	return "", &NoSkillMDError{Detail: "frontmatter 中无 name 字段"}
}
