// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

// Package resolve implements fixed-priority version resolution without heuristics (dev-design §4).
//
// This package is a pure-function layer that accepts an ls-remote snapshot (Refs) and returns a resolution or classified error.
// It performs no network or subprocess calls; package source handles Git interactions.
package resolve

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// Refs is a pure-data snapshot of remote references returned by one ls-remote call and can be constructed directly in tests.
type Refs struct {
	Tags          map[string]string // tag name to commit SHA; annotated tags use the peeled value
	Heads         map[string]string // branch name to commit SHA
	DefaultBranch string            // default branch name, such as main; empty when not advertised
	DefaultHead   string            // current default-branch commit SHA; empty when no fallback version can be resolved
}

// Kind identifies a resolution type.
type Kind int

const (
	// KindTag identifies a Git tag; Version contains its complete name.
	KindTag Kind = iota
	// KindCommit pins a commit, either an explicit 40-character SHA or the default-branch HEAD fallback.
	// Version starts empty and is filled with PseudoVersion by the engine after fetching.
	KindCommit
)

// Resolution is a resolved version.
type Resolution struct {
	Kind     Kind
	Version  string // version written to mod and lock files; the complete tag name for KindTag
	Commit   string // target commit SHA
	FetchRef string // remote ref suitable for a targeted fetch; empty for an explicit SHA
}

// Request describes one resolution request.
type Request struct {
	Repo   string // normalized repository address used only in error messages
	Subdir string // subdirectory; empty means the repository root
	Ref    string // explicit reference; empty resolves the latest version
}

// BranchError reports a ref that names a mutable remote branch and therefore cannot be locked (PRD §3.2).
type BranchError struct{ Ref string }

func (e *BranchError) Error() string {
	return fmt.Sprintf("分支不可锁定，请用 tag 或 commit SHA（%q 是分支名）", e.Ref)
}

// NotFoundError reports a ref that is neither a tag nor a 40-character SHA or branch.
type NotFoundError struct {
	Ref        string
	Candidates []string // relevant available tags in descending semver order, limited to 10
}

func (e *NotFoundError) Error() string {
	if len(e.Candidates) == 0 {
		return fmt.Sprintf("版本 %q 不存在，该仓库/子目录没有任何可用 tag", e.Ref)
	}
	return fmt.Sprintf("版本 %q 不存在。可用 tag（semver 降序，最多 10 个）: %s", e.Ref, strings.Join(e.Candidates, ", "))
}

// EmptyRepoError reports a repository with no tags and no available default-branch HEAD.
type EmptyRepoError struct{ Repo string }

func (e *EmptyRepoError) Error() string {
	return fmt.Sprintf("仓库 %s 无任何 tag，也无法解析默认分支 HEAD（空仓库或权限不足）", e.Repo)
}

// Resolve selects a version using this fixed priority:
//
//	explicit ref: tag "<subdir>/<ref>" → tag "<ref>" → 40-character hex commit → reject branch → not found
//	omitted ref: highest subdir semver tag → highest root semver tag → default-branch HEAD as a pseudo-version
func Resolve(req Request, refs *Refs) (*Resolution, error) {
	if req.Ref != "" {
		return resolveExplicit(req, refs)
	}
	return resolveLatest(req, refs)
}

func resolveExplicit(req Request, refs *Refs) (*Resolution, error) {
	// 1. Monorepo convention: automatically prefix an explicit bare version with <subdir>/ (AC-6).
	if req.Subdir != "" {
		if r := tryTag(req.Subdir+"/"+req.Ref, refs); r != nil {
			return r, nil
		}
	}
	// 2. Repository-wide tag.
	if r := tryTag(req.Ref, refs); r != nil {
		return r, nil
	}
	// 3. Pin a 40-character hex commit directly, skipping every check except tag matching.
	if IsSHA(req.Ref) {
		return &Resolution{Kind: KindCommit, Commit: req.Ref}, nil
	}
	// 4. Reject branch names using ls-remote data rather than string-pattern guesses.
	if _, ok := refs.Heads[req.Ref]; ok {
		return nil, &BranchError{Ref: req.Ref}
	}
	// 5. The version does not exist; list relevant tags.
	return nil, &NotFoundError{Ref: req.Ref, Candidates: relevantTags(req.Subdir, refs.Tags, 10)}
}

func resolveLatest(req Request, refs *Refs) (*Resolution, error) {
	// 1. Highest <subdir>/v* semantic version.
	if req.Subdir != "" {
		if tag, ok := highestSemver(prefixedTags(refs.Tags, req.Subdir+"/")); ok {
			return &Resolution{Kind: KindTag, Version: tag, Commit: refs.Tags[tag], FetchRef: "refs/tags/" + tag}, nil
		}
	}
	// 2. Highest root-level v* semantic version.
	if tag, ok := highestSemver(rootTags(refs.Tags)); ok {
		return &Resolution{Kind: KindTag, Version: tag, Commit: refs.Tags[tag], FetchRef: "refs/tags/" + tag}, nil
	}
	// 3. Default-branch HEAD as a pseudo-version generated after fetching.
	if refs.DefaultHead != "" {
		fetchRef := "HEAD"
		if refs.DefaultBranch != "" {
			fetchRef = "refs/heads/" + refs.DefaultBranch
		}
		return &Resolution{Kind: KindCommit, Commit: refs.DefaultHead, FetchRef: fetchRef}, nil
	}
	return nil, &EmptyRepoError{Repo: req.Repo}
}

func tryTag(tag string, refs *Refs) *Resolution {
	if sha, ok := refs.Tags[tag]; ok {
		return &Resolution{Kind: KindTag, Version: tag, Commit: sha, FetchRef: "refs/tags/" + tag}
	}
	return nil
}

// prefixedTags returns tags with the given prefix whose suffix is a valid semantic version.
func prefixedTags(tags map[string]string, prefix string) []string {
	var out []string
	for tag := range tags {
		rest, ok := strings.CutPrefix(tag, prefix)
		if ok && semver.IsValid(rest) {
			out = append(out, tag)
		}
	}
	return out
}

// rootTags returns valid semantic-version tags at the repository root with no slash.
func rootTags(tags map[string]string) []string {
	var out []string
	for tag := range tags {
		if !strings.Contains(tag, "/") && semver.IsValid(tag) {
			out = append(out, tag)
		}
	}
	return out
}

// relevantTags chooses NotFoundError candidates, preferring subdir-prefixed tags and then root tags.
func relevantTags(subdir string, tags map[string]string, limit int) []string {
	var cands []string
	if subdir != "" {
		cands = prefixedTags(tags, subdir+"/")
	}
	if len(cands) == 0 {
		cands = rootTags(tags)
	}
	// Descending semantic-version order.
	sort.Slice(cands, func(i, j int) bool {
		return semver.Compare(stripPrefix(cands[i]), stripPrefix(cands[j])) > 0
	})
	if len(cands) > limit {
		cands = cands[:limit]
	}
	return cands
}

// highestSemver selects the highest semantic version while deprioritizing prereleases:
// prereleases are ignored when a release exists and selected only when no release exists, matching go @latest.
func highestSemver(tags []string) (string, bool) {
	best, bestPre, found := "", false, false
	for _, tag := range tags {
		pre := semver.Prerelease(stripPrefix(tag)) != ""
		if !found || (bestPre && !pre) ||
			(bestPre == pre && semver.Compare(stripPrefix(tag), stripPrefix(best)) > 0) {
			best, bestPre, found = tag, pre, true
		}
	}
	return best, found
}

// stripPrefix removes a tag's monorepo subdirectory prefix and returns the version portion.
func stripPrefix(tag string) string {
	if i := strings.LastIndex(tag, "/"); i >= 0 {
		return tag[i+1:]
	}
	return tag
}

// IsSHA reports whether a string is a 40-character hexadecimal commit SHA.
func IsSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f') {
			return false
		}
	}
	return true
}

// PseudoVersion creates v0.0.0-<UTC commit time: yyyymmddhhmmss>-<sha12>,
// following the Go pseudo-version format (dev-design §4).
func PseudoVersion(commitTime time.Time, sha string) string {
	return fmt.Sprintf("v0.0.0-%s-%s", commitTime.UTC().Format("20060102150405"), sha[:12])
}

// IsPseudoVersion recognizes v0.0.0-<14-digit timestamp>-<12 hex digits>.
func IsPseudoVersion(v string) bool {
	return pseudoVersionRe.MatchString(v)
}

var pseudoVersionRe = regexp.MustCompile(`^v0\.0\.0-\d{14}-[0-9a-f]{12}$`)
