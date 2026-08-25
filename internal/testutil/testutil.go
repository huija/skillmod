// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

// Package testutil constructs deterministic local Git repositories accessed through file:// with no network.
package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Repo is a scripted test repository with a Work tree and a Bare repository created by Finish.
// After Finish, Work and Bare are connected through origin so tests can push commits and tags to simulate upstream changes.
type Repo struct {
	Work string
	Bare string
	URL  string
	t    *testing.T
}

func NewRepo(t *testing.T) *Repo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git 不可用")
	}
	r := &Repo{t: t, Work: filepath.Join(t.TempDir(), "work")}
	r.git("", "init", "-b", "main", r.Work)
	r.git(r.Work, "config", "user.email", "test@skillmod.dev")
	r.git(r.Work, "config", "user.name", "test")
	return r
}

// Write creates a file and any parent directories.
func (r *Repo) Write(name, content string) {
	r.t.Helper()
	p := filepath.Join(r.Work, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// WriteSkill creates a standard skill directory containing SKILL.md and an optional extra file.
func (r *Repo) WriteSkill(dir, name string, files ...string) {
	r.t.Helper()
	r.Write(dir+"/SKILL.md", "---\nname: "+name+"\ndescription: test skill\n---\n# "+name+"\n")
	for _, f := range files {
		r.Write(dir+"/"+f, "content of "+f+"\n")
	}
}

// CommitAll stages and commits all changes, returning the commit SHA.
func (r *Repo) CommitAll(msg string) string {
	r.t.Helper()
	r.git(r.Work, "add", "-A")
	r.git(r.Work, "commit", "-m", msg)
	return r.SHA("HEAD")
}

func (r *Repo) Tag(name string) {
	r.t.Helper()
	r.git(r.Work, "tag", name)
}

func (r *Repo) SHA(ref string) string {
	r.t.Helper()
	return strings.TrimSpace(r.git(r.Work, "rev-parse", ref))
}

// Finish creates the bare repository, connects origin, and returns its file:// URL.
func (r *Repo) Finish() string {
	r.t.Helper()
	return r.FinishNamed("repo")
}

// FinishNamed is like Finish but allows a custom bare-repository name for init's single-repository matching tests.
func (r *Repo) FinishNamed(name string) string {
	r.t.Helper()
	r.Bare = filepath.Join(r.t.TempDir(), name+".git")
	r.git("", "clone", "--bare", "--quiet", r.Work, r.Bare)
	r.git(r.Work, "remote", "add", "origin", r.Bare)
	r.URL = "file://" + r.Bare
	return r.URL
}

// Evolve simulates upstream changes by committing, tagging, and pushing; moving a tag simulates tampering.
func (r *Repo) Evolve(tag string, force bool) {
	r.t.Helper()
	if tag != "" {
		if force {
			r.git(r.Work, "tag", "-f", tag)
		} else {
			r.git(r.Work, "tag", tag)
		}
	}
	args := []string{"push", "origin", "main", "--tags"}
	if force {
		args = append(args, "--force")
	}
	r.git(r.Work, args...)
}

func (r *Repo) git(dir string, args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0", "LC_ALL=C",
		"GIT_CONFIG_NOSYSTEM=1", "HOME="+r.t.TempDir(), // Isolate the user's global configuration.
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}
