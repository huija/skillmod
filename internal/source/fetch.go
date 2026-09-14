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

	"go.yaml.in/yaml/v3"

	"github.com/huija/skillmod/internal/fsutil"
	"github.com/huija/skillmod/internal/i18n"
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

// SymlinkError reports a skill containing a symlink, which skillmod refuses to install.
type SymlinkError struct{ Path string }

func (e *SymlinkError) Error() string {
	return i18n.Format("source.fetch.skill_contains_symlink", e.Path)
}

// SubmoduleError reports a Git submodule that cannot be preserved byte for byte.
type SubmoduleError struct{ Path string }

func (e *SubmoduleError) Error() string {
	return i18n.Format("source.fetch.skill_contains_submodule", e.Path)
}

// NoSkillMDError reports a subtree with no SKILL.md or an unparseable frontmatter name.
type NoSkillMDError struct{ Detail string }

func (e *NoSkillMDError) Error() string {
	return i18n.Text("source.fetch.missing_skill_md") + e.Detail
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
		return nil, fmt.Errorf(i18n.Text("source.fetch.failed_parse_commit_time"), out)
	}

	// List the complete repository; -z prevents core.quotePath from escaping non-ASCII paths.
	// On the first fetch of repo@version, obtain every blob so other skills at that version can be materialized locally.
	args := []string{"ls-tree", "-r", "-z", commit}
	out, err = s.run(ctx, tmp, args...)
	if err != nil {
		return nil, err
	}
	tree := &Tree{Commit: commit, CommitTime: time.Unix(ct, 0).UTC()}
	entries, err := parseLsTree(out)
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
			return nil, fmt.Errorf(i18n.Text("source.fetch.batch_repository_blob_fetch"), prefetchErr, err)
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

// parseLsTree parses `git ls-tree -r -z` output in the form "<mode> <type> <sha>\t<path>\0"
// into entries whose paths are relative to the repository root.
func parseLsTree(out string) ([]lsEntry, error) {
	var entries []lsEntry
	for _, rec := range strings.Split(strings.TrimRight(out, "\x00"), "\x00") {
		if rec == "" {
			continue
		}
		tab := strings.IndexByte(rec, '\t')
		if tab < 0 {
			return nil, fmt.Errorf(i18n.Text("source.fetch.malformed_ls_tree_output"), rec)
		}
		meta, path := rec[:tab], rec[tab+1:]
		parts := strings.Fields(meta)
		if len(parts) != 3 {
			return nil, fmt.Errorf(i18n.Text("source.fetch.malformed_ls_tree_output"), rec)
		}
		mode, typ, sha := parts[0], parts[1], parts[2]
		if typ != "blob" && typ != "commit" {
			continue
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
	// `fetch-pack` addresses only local paths and scp-style ssh, so every https
	// remote would fail here and leave each blob to its own lazy round trip.
	// Mirror the promisor fetch Git itself uses, which works over every transport.
	cmd := exec.CommandContext(ctx, git, platformGitArgs(
		"-c", "protocol.version=2",
		"-c", "fetch.negotiationAlgorithm=noop",
		"fetch", strings.TrimSpace(remote), "--no-tags", "--no-write-fetch-head",
		"--recurse-submodules=no", "--filter=blob:none", "--stdin",
	)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C", "GIT_NO_LAZY_FETCH=1")
	cmd.Stdin = strings.NewReader(strings.Join(missing, "\n") + "\n")
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git fetch --stdin: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	remaining, err := s.missingBlobs(ctx, dir, entries)
	if err != nil {
		return err
	}
	if len(remaining) != 0 {
		return fmt.Errorf(i18n.Text("source.fetch.blobs_still_missing"), len(remaining), strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()))
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
	cmd := exec.CommandContext(ctx, git, platformGitArgs("cat-file", "--batch-check")...)
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
		return nil, fmt.Errorf(i18n.Text("source.fetch.cat_file_batch_check"), len(lines), len(ids))
	}
	var missing []string
	for i, line := range lines {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == ids[i] && fields[1] == "missing" {
			missing = append(missing, ids[i])
			continue
		}
		if len(fields) < 3 || fields[0] != ids[i] || fields[1] != "blob" {
			return nil, fmt.Errorf(i18n.Text("source.fetch.malformed_cat_file_batch"), line)
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
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(childCtx, git, platformGitArgs("cat-file", "--batch")...)
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
		_ = stdin.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf(i18n.Text("source.fetch.start_git_cat_file"), err)
	}
	go func() {
		for _, e := range entries {
			_, _ = io.WriteString(stdin, e.sha+"\n")
		}
		_ = stdin.Close()
	}()
	abort := func() {
		cancel()
		_ = stdin.Close()
		_ = cmd.Wait()
	}

	out := make(map[string][]byte, len(entries))
	r := bufio.NewReader(stdout)
	for _, e := range entries {
		header, err := r.ReadString('\n')
		if err != nil {
			abort()
			return nil, fmt.Errorf(i18n.Text("source.fetch.failed_read_blob_header"), e.sha, err)
		}
		parts := strings.Fields(strings.TrimRight(header, "\n"))
		if len(parts) != 3 || parts[1] != "blob" {
			abort()
			return nil, fmt.Errorf(i18n.Text("source.fetch.blob_unreadable"), e.sha, strings.TrimSpace(header))
		}
		size, err := strconv.Atoi(parts[2])
		if err != nil {
			abort()
			return nil, err
		}
		data := make([]byte, size)
		if _, err := io.ReadFull(r, data); err != nil {
			abort()
			return nil, fmt.Errorf(i18n.Text("source.fetch.failed_read_blob_contents"), e.sha, err)
		}
		if _, err := r.ReadByte(); err != nil { // Separator newline after the blob.
			abort()
			return nil, err
		}
		out[e.sha] = data
	}
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("git cat-file: %w", err)
	}
	return out, nil
}

// SkillMetadata describes the frontmatter fields used to identify a skill.
type SkillMetadata struct {
	Name        string
	Description string
}

// SkillMetadataFromDir reads the SKILL.md frontmatter from a directory on disk.
func SkillMetadataFromDir(dir string) (SkillMetadata, error) {
	data, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return SkillMetadata{}, &NoSkillMDError{Detail: i18n.Text("source.fetch.skill_md_missing_subtree")}
	}
	return ParseSkillMetadata(string(data))
}

// SkillNameFromDir reads the SKILL.md frontmatter name from a directory on disk for version-snapshot hits.
func SkillNameFromDir(dir string) (string, error) {
	metadata, err := SkillMetadataFromDir(dir)
	if err != nil {
		return "", err
	}
	return metadata.Name, nil
}

// ParseSkillMetadata parses the scalar name and description fields from YAML
// frontmatter. Unknown metadata fields remain available to other skill tools;
// only fields used by skillmod are decoded here.
func ParseSkillMetadata(content string) (SkillMetadata, error) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || lines[0] != "---" {
		return SkillMetadata{}, &NoSkillMDError{Detail: i18n.Text("source.fetch.missing_opening_frontmatter")}
	}
	closing := -1
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			closing = i
			break
		}
	}
	if closing < 0 {
		return SkillMetadata{}, &NoSkillMDError{Detail: i18n.Text("source.fetch.missing_closing_frontmatter")}
	}
	block := strings.Join(lines[1:closing], "\n")
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(block), &document); err != nil {
		return SkillMetadata{}, invalidFrontmatter(err)
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return SkillMetadata{}, invalidFrontmatter(nil)
	}
	root := document.Content[0]
	metadata := SkillMetadata{}
	seen := map[string]bool{}
	for i := 0; i < len(root.Content); i += 2 {
		key, value := root.Content[i], root.Content[i+1]
		if key.Kind != yaml.ScalarNode {
			return SkillMetadata{}, invalidFrontmatter(nil)
		}
		if key.Value != "name" && key.Value != "description" {
			continue
		}
		if seen[key.Value] || value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
			return SkillMetadata{}, invalidFrontmatter(nil)
		}
		seen[key.Value] = true
		switch key.Value {
		case "name":
			metadata.Name = value.Value
		case "description":
			metadata.Description = value.Value
		}
	}
	if metadata.Name == "" {
		return SkillMetadata{}, &NoSkillMDError{Detail: i18n.Text("source.fetch.frontmatter_has_name_field")}
	}
	if err := fsutil.ValidName(metadata.Name); err != nil {
		return SkillMetadata{}, &NoSkillMDError{Detail: i18n.Format("source.fetch.skill_name_invalid", metadata.Name, err)}
	}
	return metadata, nil
}

func invalidFrontmatter(err error) error {
	detail := i18n.Text("source.fetch.invalid_frontmatter")
	if err != nil {
		detail += err.Error()
	}
	return &NoSkillMDError{Detail: detail}
}
