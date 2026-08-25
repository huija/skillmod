// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package source

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/huija/skillmod/internal/filelock"
)

func vcsKey(repo string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte("git:"+RepoIdentity(repo))))
}

// openRepo opens one persistent bare repository and holds its cross-process lock.
// cleanup releases the lock and also removes the root when an ephemeral root was used.
func (s *Source) openRepo(ctx context.Context, repo string) (dir string, cleanup func(), err error) {
	root := s.VCSRoot
	ephemeral := false
	if root == "" {
		root, err = os.MkdirTemp("", "skillmod-vcs-*")
		if err != nil {
			return "", nil, err
		}
		ephemeral = true
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		if ephemeral {
			os.RemoveAll(root)
		}
		return "", nil, err
	}
	key := vcsKey(repo)
	dir = filepath.Join(root, key)
	unlock, err := filelock.Lock(dir + ".lock")
	if err != nil {
		if ephemeral {
			os.RemoveAll(root)
		}
		return "", nil, err
	}
	cleanup = func() {
		unlock()
		if ephemeral {
			_ = os.RemoveAll(root)
		}
	}

	wantInfo := "git:" + RepoIdentity(repo)
	info, infoErr := os.ReadFile(dir + ".info")
	st, dirErr := os.Stat(dir)
	if infoErr == nil && dirErr == nil && st.IsDir() {
		haveRepo := strings.TrimPrefix(strings.TrimSpace(string(info)), "git:")
		if RepoIdentity(haveRepo) != RepoIdentity(repo) {
			cleanup()
			return "", nil, fmt.Errorf("VCS 缓存身份冲突: %s", dir)
		}
		if err := s.ensureOrigin(ctx, dir, repo); err != nil {
			cleanup()
			return "", nil, err
		}
		if strings.TrimSpace(string(info)) != wantInfo {
			if err := os.WriteFile(dir+".info", []byte(wantInfo+"\n"), 0o600); err != nil {
				cleanup()
				return "", nil, err
			}
		}
		return dir, cleanup, nil
	}
	if infoErr != nil && !errors.Is(infoErr, fs.ErrNotExist) {
		cleanup()
		return "", nil, infoErr
	}
	if dirErr != nil && !errors.Is(dirErr, fs.ErrNotExist) {
		cleanup()
		return "", nil, dirErr
	}

	// Missing or partial initialization: rebuild this exact content-addressed slot.
	if err := os.RemoveAll(dir); err != nil {
		cleanup()
		return "", nil, err
	}
	if _, err := s.run(ctx, "", "init", "--bare", "--quiet", dir); err != nil {
		cleanup()
		return "", nil, err
	}
	if _, err := s.run(ctx, dir, "remote", "add", "origin", repo); err != nil {
		cleanup()
		return "", nil, err
	}
	// Mark origin as a promisor remote so cat-file can lazily obtain selected blobs.
	if _, err := s.run(ctx, dir, "config", "remote.origin.promisor", "true"); err != nil {
		cleanup()
		return "", nil, err
	}
	if _, err := s.run(ctx, dir, "config", "remote.origin.partialclonefilter", "blob:none"); err != nil {
		cleanup()
		return "", nil, err
	}
	if err := os.WriteFile(dir+".info", []byte(wantInfo+"\n"), 0o600); err != nil {
		cleanup()
		return "", nil, err
	}
	return dir, cleanup, nil
}

func (s *Source) ensureOrigin(ctx context.Context, dir, repo string) error {
	current, err := s.run(ctx, dir, "remote", "get-url", "origin")
	if err != nil {
		return err
	}
	if strings.TrimSpace(current) == repo {
		return nil
	}
	_, err = s.run(ctx, dir, "remote", "set-url", "origin", repo)
	return err
}

func (s *Source) hasCommit(ctx context.Context, dir, commit string) bool {
	git := s.Git
	if git == "" {
		git = "git"
	}
	cmd := exec.CommandContext(ctx, git, "cat-file", "-e", commit+"^{commit}")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C", "GIT_NO_LAZY_FETCH=1")
	return cmd.Run() == nil
}

func refspec(commit, fetchRef string) string {
	switch {
	case fetchRef == "HEAD":
		return "HEAD:refs/skillmod/" + commit
	case strings.HasPrefix(fetchRef, "refs/"):
		return "+" + fetchRef + ":" + fetchRef
	default:
		return commit + ":refs/skillmod/" + commit
	}
}

func (s *Source) fetchTarget(ctx context.Context, dir, commit, fetchRef string) error {
	if s.hasCommit(ctx, dir, commit) {
		return nil
	}
	spec := refspec(commit, fetchRef)
	filtered := []string{"-c", "protocol.version=2", "fetch", "-f", "--depth=1", "--filter=blob:none", "--no-tags", "--quiet", "origin", spec}
	if _, err := s.run(ctx, dir, filtered...); err != nil {
		plain := []string{"-c", "protocol.version=2", "fetch", "-f", "--depth=1", "--no-tags", "--quiet", "origin", spec}
		_, _ = s.run(ctx, dir, plain...)
	}
	if s.hasCommit(ctx, dir, commit) {
		return nil
	}

	// Some servers reject direct SHA fetches. Fall back to advertised heads/tags,
	// including their history, matching Go's direct VCS strategy.
	allRefs := []string{"fetch", "-f", "--filter=blob:none", "--quiet", "origin", "+refs/heads/*:refs/heads/*", "+refs/tags/*:refs/tags/*"}
	if _, err := s.run(ctx, dir, allRefs...); err != nil {
		plain := []string{"fetch", "-f", "--quiet", "origin", "+refs/heads/*:refs/heads/*", "+refs/tags/*:refs/tags/*"}
		if _, plainErr := s.run(ctx, dir, plain...); plainErr != nil {
			return fmt.Errorf("抓取 %s@%s 失败: %w", repoOrigin(dir), shortSHA(commit), plainErr)
		}
	}
	if !s.hasCommit(ctx, dir, commit) {
		return fmt.Errorf("commit %s 不在远端公开 heads/tags 历史中", commit)
	}
	return nil
}

func repoOrigin(dir string) string {
	data, err := os.ReadFile(dir + ".info")
	if err != nil {
		return dir
	}
	return strings.TrimPrefix(strings.TrimSpace(string(data)), "git:")
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
