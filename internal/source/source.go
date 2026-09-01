// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

// Package source wraps subprocess calls to the system Git executable.
// It avoids go-git so SSH keys, credential helpers, and proxies are inherited from the user's environment.
package source

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/huija/skillmod/internal/i18n"
	"github.com/huija/skillmod/internal/resolve"
)

// RepoErrorKind classifies remote access failures, distinguishing not found from authentication failures as required by PRD §3.2.
type RepoErrorKind int

const (
	// RepoNotFound indicates a missing repository: exit 128 with "not found" in stderr.
	RepoNotFound RepoErrorKind = iota
	// RepoAuth indicates failed authentication, with "Authentication" or "Permission denied" in stderr.
	RepoAuth
	// RepoOther covers other Git errors, including network and protocol failures.
	RepoOther
)

// RepoError describes one failed remote access.
type RepoError struct {
	Kind   RepoErrorKind
	Repo   string
	Stderr string // trimmed original Git error output for diagnostics
}

func (e *RepoError) Error() string {
	switch e.Kind {
	case RepoNotFound:
		return i18n.Format("repository not found: %s (check the address)", e.Repo)
	case RepoAuth:
		return i18n.Format("repository authentication failed: %s (configure Git credentials with gh auth login or an SSH key)", e.Repo)
	default:
		return i18n.Format("failed to access repository: %s: %s", e.Repo, e.Stderr)
	}
}

// Source invokes the system Git executable and is usable as a zero value.
type Source struct {
	// Git is the executable path; empty means "git".
	Git string
	// VCSRoot is the persistent bare-repository root; when empty, Fetch uses a temporary directory.
	VCSRoot string
}

// run executes a Git subprocess with a consistent environment:
// GIT_TERMINAL_PROMPT=0 prevents password prompts from hanging an agent session, and LC_ALL=C makes errors parseable.
// An empty dir means the current directory.
func (s *Source) run(ctx context.Context, dir string, args ...string) (string, error) {
	git := s.Git
	if git == "" {
		git = "git"
	}
	cmd := exec.CommandContext(ctx, git, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", mapGitError(err, stderr.String(), repoArg(args))
	}
	return stdout.String(), nil
}

// repoArg makes a best effort to find a repository address in the arguments for error reporting.
func repoArg(args []string) string {
	for i := len(args) - 1; i >= 0; i-- {
		if !strings.HasPrefix(args[i], "-") {
			return args[i]
		}
	}
	return ""
}

func mapGitError(err error, stderr, repo string) error {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 128 {
		return fmt.Errorf(i18n.Text("git execution failed: %w"), err)
	}
	kind := RepoOther
	switch {
	case strings.Contains(stderr, "not found"),
		strings.Contains(stderr, "does not appear to be a git repository"):
		kind = RepoNotFound
	case strings.Contains(stderr, "Authentication"),
		strings.Contains(stderr, "Permission denied"):
		kind = RepoAuth
	}
	stderr = strings.TrimSpace(stderr)
	if len(stderr) > 300 {
		stderr = stderr[:300] + "…"
	}
	return &RepoError{Kind: kind, Repo: repo, Stderr: stderr}
}

// Refs obtains a remote-reference snapshot with one ls-remote call.
// It intentionally omits --heads and --tags filters so one call returns tags, heads, and the HEAD symref.
// Filters would require two calls, while GitHub does not advertise hidden refs such as refs/pull/*, so they add no noise.
func (s *Source) Refs(ctx context.Context, repo string) (*resolve.Refs, error) {
	out, err := s.run(ctx, "", "ls-remote", "--symref", repo)
	if err != nil {
		return nil, err
	}
	return ParseLsRemote(out), nil
}

// ParseLsRemote parses `git ls-remote --symref` output.
// An annotated tag uses the commit from its peeled ^{} line; a lightweight tag is already a commit.
func ParseLsRemote(out string) *resolve.Refs {
	refs := &resolve.Refs{
		Tags:  map[string]string{},
		Heads: map[string]string{},
	}
	for line := range strings.Lines(out) {
		fields := strings.SplitN(strings.TrimRight(line, "\n"), "\t", 2)
		if len(fields) != 2 {
			continue
		}
		sha, name := fields[0], fields[1]
		if target, ok := strings.CutPrefix(sha, "ref: "); ok {
			// Symref line: ref: refs/heads/main\tHEAD
			if name == "HEAD" {
				refs.DefaultBranch = strings.TrimPrefix(target, "refs/heads/")
			}
			continue
		}
		switch {
		case name == "HEAD":
			refs.DefaultHead = sha
		case strings.HasPrefix(name, "refs/heads/"):
			refs.Heads[strings.TrimPrefix(name, "refs/heads/")] = sha
		case strings.HasPrefix(name, "refs/tags/"):
			tag := strings.TrimPrefix(name, "refs/tags/")
			if commit, peeled := strings.CutSuffix(tag, "^{}"); peeled {
				refs.Tags[commit] = sha // For an annotated tag, the peeled line replaces the tag-object line.
			} else if _, exists := refs.Tags[tag]; !exists {
				refs.Tags[tag] = sha
			}
		}
	}
	return refs
}
