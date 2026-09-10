// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

// Package address parses the <repo>[//<subdir>][@<ref>] addressing syntax.
package address

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/huija/skillmod/internal/i18n"
)

// Address is a parsed skill address.
type Address struct {
	Repo   string // normalized Git address; bare paths include an https:// prefix
	Subdir string // subdirectory within the repository; empty means the repository root
	Ref    string // explicit version reference; empty asks package resolve for the latest version
}

// String returns the canonical address.
func (a *Address) String() string {
	s := a.Repo
	if a.Subdir != "" {
		s += "//" + a.Subdir
	}
	if a.Ref != "" {
		s += "@" + a.Ref
	}
	return s
}

// scpLike matches git@host:path addresses, where @ is not a version separator.
var scpLike = regexp.MustCompile(`^[^/@\s:]+@[^/@\s:]+:`)

// Parse validates and normalizes an address without accessing the network.
// Branch names are rejected later by package resolve using ls-remote data rather than string heuristics.
func Parse(raw string) (*Address, error) {
	if raw == "" {
		return nil, fmt.Errorf("%s", i18n.Text("address.empty_repository"))
	}

	base, ref, err := splitRef(raw)
	if err != nil {
		return nil, err
	}

	repo, subdir, err := splitSubdir(base)
	if err != nil {
		return nil, err
	}
	if repo == "" {
		return nil, fmt.Errorf(i18n.Text("address.missing_repository_address"), raw)
	}
	if strings.ContainsAny(repo, " \t") {
		return nil, fmt.Errorf(i18n.Text("address.repository_whitespace"), repo)
	}
	repo = normalizeRepo(repo)

	if subdir != "" {
		if subdir, err = cleanSubdir(subdir); err != nil {
			return nil, err
		}
	}
	return &Address{Repo: repo, Subdir: subdir, Ref: ref}, nil
}

// splitRef splits at the final @ that acts as a version separator. Explicit
// versions are deliberately limited to a tag name without slashes or a commit
// SHA; slash-bearing forms remain part of the address.
func splitRef(s string) (base, ref string, err error) {
	i := strings.LastIndexByte(s, '@')
	if i < 0 {
		return s, "", nil
	}
	suffix := s[i+1:]
	if suffix == "" {
		return "", "", fmt.Errorf(i18n.Text("address.missing_version_reference"), s)
	}
	if strings.ContainsAny(suffix, ":/") {
		return s, "", nil // scp-like form with no ref
	}
	if s[:i] == "" {
		return "", "", fmt.Errorf(i18n.Text("address.missing_repository_address"), s)
	}
	ref = s[i+1:]
	if strings.ContainsAny(ref, " \t") {
		return "", "", fmt.Errorf(i18n.Text("address.version_whitespace"), ref)
	}
	return s[:i], ref, nil
}

// splitSubdir splits at the // subdirectory separator while skipping :// in a URL scheme.
func splitSubdir(s string) (repo, subdir string, err error) {
	off := 0
	if i := strings.Index(s, "://"); i >= 0 {
		off = i + len("://")
	}
	rel := strings.Index(s[off:], "//")
	if rel < 0 {
		return s, "", nil
	}
	repo, subdir = s[:off+rel], s[off+rel+2:]
	if subdir == "" {
		return "", "", fmt.Errorf(i18n.Text("address.missing_subdirectory"), s)
	}
	return repo, subdir, nil
}

// normalizeRepo adds https:// to bare paths and preserves full URLs and scp-like addresses.
func normalizeRepo(repo string) string {
	if strings.Contains(repo, "://") || scpLike.MatchString(repo) {
		return repo
	}
	return "https://" + repo
}

// cleanSubdir validates a canonical slash-separated Git path with no redundant or escaping segments.
func cleanSubdir(s string) (string, error) {
	if strings.Contains(s, "\\") {
		return "", fmt.Errorf(i18n.Text("address.subdirectory_separators"), s)
	}
	if strings.Contains(s, "@") {
		return "", fmt.Errorf(i18n.Text("address.subdirectory_at_sign"), s)
	}
	c := path.Clean(s)
	if c != s || c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return "", fmt.Errorf(i18n.Text("address.non_canonical_subdirectory"), s, strings.TrimPrefix(c, "./"))
	}
	return s, nil
}
