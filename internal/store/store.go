// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

// Package store implements skillmod's persistent, Go-module-style local store.
//
// The store has one user-visible immutable layer plus internal mutable metadata:
//   - pkg/mod/<host>/<owner>/<repo>@<version>: one full repository snapshot
//   - pkg/mod/cache/vcs: one mutable bare Git repository per logical remote
//   - pkg/mod/cache/download: repo metadata, refs and per-skill resolutions
//   - pkg/mod/cache/locks: cross-process store locks
package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/huija/skillmod/internal/dirhash"
	"github.com/huija/skillmod/internal/filelock"
	"github.com/huija/skillmod/internal/i18n"
	"github.com/huija/skillmod/internal/resolve"
	"github.com/huija/skillmod/internal/source"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

const HomeEnv = "SKILLMOD_HOME"

// Store is the persistent user-level module store.
type Store struct {
	root     string
	readOnly bool
}

// Open opens the default persistent store. SKILLMOD_HOME, when set, must be absolute.
func Open() (*Store, error) {
	if root := os.Getenv(HomeEnv); root != "" {
		if !filepath.IsAbs(root) {
			return nil, fmt.Errorf(i18n.Text("%s must be an absolute path: %q"), HomeEnv, root)
		}
		return newStore(filepath.Clean(root), true), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf(i18n.Text("determine user home directory: %w"), err)
	}
	return newStore(filepath.Join(home, ".agents", "skillmod"), true), nil
}

// New creates a writable store rooted at root. It is primarily used by tests.
func New(root string) *Store { return newStore(root, false) }

func newStore(root string, readOnly bool) *Store {
	return &Store{root: filepath.Clean(root), readOnly: readOnly}
}

// Root returns the global store root.
func (s *Store) Root() string { return s.root }

// ModRoot is the Go-style module cache root containing readable repo@version trees.
func (s *Store) ModRoot() string { return filepath.Join(s.root, "pkg", "mod") }

// CacheRoot contains implementation-private mutable metadata.
func (s *Store) CacheRoot() string { return filepath.Join(s.ModRoot(), "cache") }

// VCSRoot returns the directory containing persistent bare Git repositories.
func (s *Store) VCSRoot() string { return filepath.Join(s.CacheRoot(), "vcs") }

// SnapshotInfo records the immutable identity and integrity of one materialized repo.
type SnapshotInfo struct {
	Repo       string   `json:"repo"`
	Version    string   `json:"version"`
	Commit     string   `json:"commit"`
	Treehash   string   `json:"treehash"`
	Symlinks   []string `json:"symlinks,omitempty"`
	Submodules []string `json:"submodules,omitempty"`
}

// Snapshot is a verified full-repository snapshot in pkg/mod.
type Snapshot struct {
	Info       SnapshotInfo
	ContentDir string
}

// SnapshotConflictError means an immutable source/version identity was previously
// stored with different provenance or content.
type SnapshotConflictError struct {
	Path       string
	Have, Want SnapshotInfo
}

func (e *SnapshotConflictError) Error() string {
	return i18n.Format("version snapshot conflict; refusing to overwrite %s\nexisting:  %s @ %s (%s)\nrequested: %s @ %s (%s)\nPossible cause: the remote tag moved or the contents of the same version were rewritten",
		e.Path,
		e.Have.Version, shortCommit(e.Have.Commit), displayHash(e.Have.Treehash),
		e.Want.Version, shortCommit(e.Want.Commit), displayHash(e.Want.Treehash))
}

// CorruptError reports a damaged or locally modified persistent snapshot.
type CorruptError struct {
	Path   string
	Detail string
}

func (e *CorruptError) Error() string {
	return i18n.Format("global version snapshot is corrupt or modified: %s (%s)", e.Path, e.Detail)
}

func shortCommit(commit string) string {
	if commit == "" {
		return i18n.Text("commit pending resolution")
	}
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

func displayHash(hash string) string {
	if hash == "" {
		return i18n.Text("hash pending verification")
	}
	return hash
}

func hashKey(parts ...string) string {
	h := sha256.New()
	for i, part := range parts {
		if i > 0 {
			h.Write([]byte{0})
		}
		h.Write([]byte(part))
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func versionKey(version string) (prefix, key string, err error) {
	semverVersion := version
	if i := strings.LastIndexByte(semverVersion, '/'); i >= 0 {
		prefix = semverVersion[:i]
		semverVersion = semverVersion[i+1:]
	}
	if !semver.IsValid(semverVersion) {
		return "", "", fmt.Errorf(i18n.Text("version is not a canonical semver or pseudo-version: %q"), version)
	}
	key, err = module.EscapeVersion(semverVersion)
	return prefix, key, err
}

// escapePathSegment uses Go's readable !upper convention and percent-escapes
// bytes that are unsafe in a cross-platform path component.
func escapePathSegment(segment string) string {
	var b strings.Builder
	for i := 0; i < len(segment); i++ {
		c := segment[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '.', c == '-', c == '_':
			b.WriteByte(c)
		case c >= 'A' && c <= 'Z':
			b.WriteByte('!')
			b.WriteByte(c + ('a' - 'A'))
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	if b.Len() == 0 {
		return "_"
	}
	return b.String()
}

// repoPath returns a credential-free, readable path such as
// github.com/anthropics/skills. Non-web and local repositories live below a
// scheme-prefixed namespace but remain reversible and inspectable.
func repoPath(repo string) (string, error) {
	identity := source.RepoIdentity(repo)
	u, err := url.Parse(identity)
	if err != nil {
		return "", fmt.Errorf(i18n.Text("parse repository identity %q: %w"), identity, err)
	}
	var raw []string
	if u.Scheme != "" {
		switch u.Scheme {
		case "https":
			raw = append(raw, u.Host)
		default:
			raw = append(raw, "_"+u.Scheme)
			if u.Host != "" {
				raw = append(raw, u.Host)
			}
		}
		cleaned := strings.Trim(path.Clean("/"+strings.TrimPrefix(u.Path, "/")), "/")
		if cleaned != "" && cleaned != "." {
			raw = append(raw, strings.Split(cleaned, "/")...)
		}
	} else {
		raw = append(raw, "_repo")
		cleaned := strings.Trim(path.Clean("/"+strings.TrimPrefix(identity, "/")), "/")
		if cleaned != "" && cleaned != "." {
			raw = append(raw, strings.Split(cleaned, "/")...)
		}
	}
	if len(raw) < 2 {
		return "", fmt.Errorf(i18n.Text("repository identity has no project path: %q"), identity)
	}
	escaped := make([]string, 0, len(raw))
	for _, segment := range raw {
		if segment == "" || segment == "." || segment == ".." {
			return "", fmt.Errorf(i18n.Text("repository identity contains an unsafe path segment: %q"), identity)
		}
		escaped = append(escaped, escapePathSegment(segment))
	}
	return filepath.Join(escaped...), nil
}

func snapshotVersion(version string) (string, error) {
	prefix, v, err := versionKey(version)
	if err != nil {
		return "", err
	}
	if prefix == "" {
		return v, nil
	}
	return escapePathSegment(prefix) + "%2F" + v, nil
}

// SnapshotPath returns the readable directory for a repo/version identity.
func (s *Store) SnapshotPath(repo, version string) (string, error) {
	rel, err := repoPath(repo)
	if err != nil {
		return "", err
	}
	v, err := snapshotVersion(version)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.ModRoot(), rel+"@"+v), nil
}

func (s *Store) snapshotInfoPath(repo, version string) (string, error) {
	rel, err := repoPath(repo)
	if err != nil {
		return "", err
	}
	v, err := snapshotVersion(version)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.CacheRoot(), "download", rel, "@v", v+".info"), nil
}

// FindSnapshotVersionByCommit finds any already materialized version of the
// same immutable repo commit. It lets a second skill addressed by commit reuse
// the repo snapshot without consulting Git or the network.
func (s *Store) FindSnapshotVersionByCommit(repo, commit string) (string, bool, error) {
	rel, err := repoPath(repo)
	if err != nil {
		return "", false, err
	}
	dir := filepath.Join(s.CacheRoot(), "download", rel, "@v")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".info") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return "", false, err
		}
		var info SnapshotInfo
		if err := json.Unmarshal(data, &info); err != nil {
			return "", false, fmt.Errorf(i18n.Text("cannot parse version metadata %s: %w"), filepath.Join(dir, entry.Name()), err)
		}
		if source.RepoIdentity(info.Repo) != source.RepoIdentity(repo) {
			return "", false, fmt.Errorf(i18n.Text("version metadata repository identity mismatch: %s"), filepath.Join(dir, entry.Name()))
		}
		expectedPath, err := s.snapshotInfoPath(repo, info.Version)
		if err != nil || filepath.Clean(expectedPath) != filepath.Join(dir, entry.Name()) {
			return "", false, fmt.Errorf(i18n.Text("version metadata version does not match its file name: %s"), filepath.Join(dir, entry.Name()))
		}
		if info.Commit == commit {
			return info.Version, true, nil
		}
	}
	return "", false, nil
}

// GetSnapshot loads and hashes an existing full repo snapshot. fs.ErrNotExist
// means both the source tree and its metadata are absent.
func (s *Store) GetSnapshot(repo, version string) (*Snapshot, error) {
	dir, err := s.SnapshotPath(repo, version)
	if err != nil {
		return nil, err
	}
	infoPath, err := s.snapshotInfoPath(repo, version)
	if err != nil {
		return nil, err
	}
	data, infoErr := os.ReadFile(infoPath)
	_, dirErr := os.Stat(dir)
	if errors.Is(infoErr, fs.ErrNotExist) && errors.Is(dirErr, fs.ErrNotExist) {
		return nil, fs.ErrNotExist
	}
	if infoErr != nil {
		return nil, &CorruptError{Path: dir, Detail: i18n.Text("version metadata is unreadable: ") + infoErr.Error()}
	}
	if dirErr != nil {
		return nil, &CorruptError{Path: dir, Detail: i18n.Text("version directory is unreadable: ") + dirErr.Error()}
	}
	var info SnapshotInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, &CorruptError{Path: dir, Detail: i18n.Text("cannot parse version metadata: ") + err.Error()}
	}
	if source.RepoIdentity(info.Repo) != source.RepoIdentity(repo) || info.Version != version {
		return nil, &CorruptError{Path: dir, Detail: i18n.Text("repository/version in version metadata does not match the directory identity")}
	}
	h, err := dirhash.HashDir(dir)
	if err != nil {
		return nil, &CorruptError{Path: dir, Detail: i18n.Text("version directory cannot be verified: ") + err.Error()}
	}
	if h != info.Treehash {
		return nil, &CorruptError{Path: dir, Detail: i18n.Format("treehash mismatch: recorded %s, computed %s", info.Treehash, h)}
	}
	return &Snapshot{Info: info, ContentDir: dir}, nil
}

// PutSnapshot atomically creates an immutable version-addressed full repo snapshot.
func (s *Store) PutSnapshot(info SnapshotInfo, files []source.File) (*Snapshot, error) {
	info.Repo = source.RepoIdentity(info.Repo)
	dst, err := s.SnapshotPath(info.Repo, info.Version)
	if err != nil {
		return nil, err
	}
	infoPath, err := s.snapshotInfoPath(info.Repo, info.Version)
	if err != nil {
		return nil, err
	}
	unlock, err := filelock.Lock(filepath.Join(s.CacheRoot(), "locks", hashKey("snapshot", dst)+".lock"))
	if err != nil {
		return nil, err
	}
	defer unlock()

	if existing, err := s.GetSnapshot(info.Repo, info.Version); err == nil {
		if existing.Info.Commit != info.Commit || existing.Info.Treehash != info.Treehash ||
			!slices.Equal(existing.Info.Symlinks, info.Symlinks) || !slices.Equal(existing.Info.Submodules, info.Submodules) {
			return nil, &SnapshotConflictError{Path: dst, Have: existing.Info, Want: info}
		}
		return existing, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	parent := filepath.Dir(dst)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp(parent, ".skillmod-tmp-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	if err := writeTree(tmp, files); err != nil {
		return nil, err
	}
	gotHash, err := dirhash.HashDir(tmp)
	if err != nil {
		return nil, err
	}
	if gotHash != info.Treehash {
		return nil, fmt.Errorf(i18n.Text("treehash verification failed before writing version snapshot: expected %s, computed %s"), info.Treehash, gotHash)
	}
	meta, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return nil, err
	}
	meta = append(meta, '\n')
	if err := os.MkdirAll(filepath.Dir(infoPath), 0o700); err != nil {
		return nil, err
	}
	metaTmp, err := os.CreateTemp(filepath.Dir(infoPath), ".info-*.tmp")
	if err != nil {
		return nil, err
	}
	metaTmpName := metaTmp.Name()
	defer os.Remove(metaTmpName)
	if err := metaTmp.Chmod(0o600); err != nil {
		metaTmp.Close()
		return nil, err
	}
	if _, err := metaTmp.Write(meta); err != nil {
		metaTmp.Close()
		return nil, err
	}
	if err := metaTmp.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return nil, fmt.Errorf(i18n.Text("failed to commit version snapshot to disk: %w"), err)
	}
	if err := replaceFile(metaTmpName, infoPath); err != nil {
		_ = os.RemoveAll(dst)
		return nil, fmt.Errorf(i18n.Text("failed to commit version metadata to disk: %w"), err)
	}
	if s.readOnly {
		if err := os.Chmod(infoPath, 0o444); err != nil {
			return nil, fmt.Errorf(i18n.Text("failed to make version metadata read-only: %w"), err)
		}
	}
	if s.readOnly {
		if err := makeReadOnly(dst); err != nil {
			return nil, fmt.Errorf(i18n.Text("failed to make version snapshot read-only: %w"), err)
		}
	}
	return &Snapshot{Info: info, ContentDir: dst}, nil
}

func writeTree(dir string, files []source.File) error {
	for _, f := range files {
		rel := filepath.Clean(filepath.FromSlash(f.Path))
		if rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf(i18n.Text("unsafe skill file path: %q"), f.Path)
		}
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		mode := fs.FileMode(0o644)
		if f.Exec {
			mode = 0o755
		}
		if err := os.WriteFile(p, f.Data, mode); err != nil {
			return err
		}
	}
	return nil
}

func makeReadOnly(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.Chmod(path, 0o555)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		mode := fs.FileMode(0o444)
		if info.Mode().Perm()&0o100 != 0 {
			mode = 0o555
		}
		return os.Chmod(path, mode)
	})
}

type repoRefsRecord struct {
	Repo string       `json:"repo"`
	Refs resolve.Refs `json:"refs"`
}

func (s *Store) repoRefsPath(repo string) string {
	rel, err := repoPath(repo)
	if err != nil {
		return filepath.Join(s.CacheRoot(), "refs", hashKey("refs", source.RepoIdentity(repo))+".json")
	}
	return filepath.Join(s.CacheRoot(), "download", rel, "@v", "refs.json")
}

// GetRepoRefs returns the last complete ls-remote snapshot for a logical repo.
// Callers may use positive exact-tag matches locally; latest/update must refresh.
func (s *Store) GetRepoRefs(repo string) (*resolve.Refs, bool, error) {
	p := s.repoRefsPath(repo)
	data, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var rec repoRefsRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, false, fmt.Errorf(i18n.Text("repository reference cache is corrupt %s: %w"), p, err)
	}
	if source.RepoIdentity(rec.Repo) != source.RepoIdentity(repo) {
		return nil, false, fmt.Errorf(i18n.Text("repository reference cache identity mismatch: %s"), p)
	}
	if rec.Refs.Tags == nil {
		rec.Refs.Tags = map[string]string{}
	}
	if rec.Refs.Heads == nil {
		rec.Refs.Heads = map[string]string{}
	}
	return &rec.Refs, true, nil
}

// PutRepoRefs atomically records one complete ls-remote snapshot. Equal content
// is left untouched so multiple skills from the same repo do not churn the file.
func (s *Store) PutRepoRefs(repo string, refs *resolve.Refs) error {
	if refs == nil {
		return fmt.Errorf("%s", i18n.Text("repository reference cache cannot be nil"))
	}
	p := s.repoRefsPath(repo)
	rec := repoRefsRecord{Repo: source.RepoIdentity(repo), Refs: *refs}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	unlock, err := filelock.Lock(p + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	if existing, err := os.ReadFile(p); err == nil && bytes.Equal(existing, data) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".refs-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return replaceFile(tmpName, p)
}

// ResolveEntry is a persisted ref resolution used for offline get and pseudo-versions.
type ResolveEntry struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Dirhash string `json:"dirhash"`
}

type resolveRecord struct {
	Repo   string `json:"repo"`
	Subdir string `json:"subdir,omitempty"`
	Ref    string `json:"ref"`
	ResolveEntry
}

func (s *Store) resolvePath(repo, subdir, ref string) string {
	rel, err := repoPath(repo)
	if err != nil {
		return filepath.Join(s.CacheRoot(), "resolve", hashKey("resolve", source.RepoIdentity(repo), subdir, ref)+".json")
	}
	return filepath.Join(s.CacheRoot(), "download", rel, "@v", "resolve", hashKey("resolve", subdir, ref)+".json")
}

// GetResolved returns a previously recorded ref resolution.
func (s *Store) GetResolved(repo, subdir, ref string) (ResolveEntry, bool, error) {
	p := s.resolvePath(repo, subdir, ref)
	data, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return ResolveEntry{}, false, nil
	}
	if err != nil {
		return ResolveEntry{}, false, err
	}
	var rec resolveRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return ResolveEntry{}, false, fmt.Errorf(i18n.Text("resolution cache index is corrupt %s: %w"), p, err)
	}
	if source.RepoIdentity(rec.Repo) != source.RepoIdentity(repo) || rec.Subdir != subdir || rec.Ref != ref {
		return ResolveEntry{}, false, fmt.Errorf(i18n.Text("resolution cache index identity mismatch: %s"), p)
	}
	return rec.ResolveEntry, true, nil
}

// PutResolved atomically records a ref resolution in a per-entry file.
func (s *Store) PutResolved(repo, subdir, ref string, entry ResolveEntry) error {
	p := s.resolvePath(repo, subdir, ref)
	unlock, err := filelock.Lock(p + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	rec := resolveRecord{Repo: source.RepoIdentity(repo), Subdir: subdir, Ref: ref, ResolveEntry: entry}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".resolve-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return replaceFile(tmpName, p)
}
