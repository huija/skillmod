// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

// Package engine orchestrates transactional logic for get, sync, verify, update, prune, list, and init.
// Its core principle is validate before writing in two phases: phase 1 never writes project files,
// so network, hash, and conflict failures happen before phase 2 performs only local copies.
package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/huija/skillmod/internal/address"
	"github.com/huija/skillmod/internal/config"
	"github.com/huija/skillmod/internal/dirhash"
	"github.com/huija/skillmod/internal/i18n"
	"github.com/huija/skillmod/internal/install"
	"github.com/huija/skillmod/internal/modfile"
	"github.com/huija/skillmod/internal/resolve"
	"github.com/huija/skillmod/internal/source"
	"github.com/huija/skillmod/internal/store"
	"github.com/huija/skillmod/internal/ui"
	"golang.org/x/mod/semver"
)

// Engine is the business core; the CLI layer only handles I/O.
type Engine struct {
	Root   string         // project root directory
	Source *source.Source // Git interactions
	Store  *store.Store   // persistent version snapshots and resolution index
	Config *config.Config // machine-level settings such as platform selection
}

// IO contains the input, output, and confirmation channels for one command run.
type IO struct {
	Out, Err io.Writer
	Confirm  ui.Confirmer // nil means non-interactive and applies safe conflict defaults
	Progress ui.Progress  // nil disables interactive activity updates
	Yes      bool         // skip confirmation in CI
	DryRun   bool         // report the plan without writing files
}

func (io IO) printf(format string, args ...any) {
	if io.Out != nil {
		fmt.Fprintf(io.Out, format+"\n", args...)
	}
}

func (io IO) setProgress(messages ...string) {
	if io.Progress != nil {
		io.Progress.Set(messages...)
	}
}

func (io IO) stopProgress() {
	if io.Progress != nil {
		io.Progress.Stop()
	}
}

// EntryReport is one entry's result and the structured unit emitted by --json.
type EntryReport struct {
	Name    string   `json:"name"`
	Source  string   `json:"source,omitempty"`
	Action  string   `json:"action"` // install/update/keep/skip/conflict/drift/local/stale/...
	Version string   `json:"version,omitempty"`
	Note    string   `json:"note,omitempty"`
	Targets []string `json:"targets,omitempty"`
}

// Report is the structured result of a command.
type Report struct {
	Action  string        `json:"action"`
	Entries []EntryReport `json:"entries"`
	Notes   []string      `json:"notes,omitempty"`
}

// DriftError reports drift detected by verify and maps to CLI exit code 2 for AC-12.
type DriftError struct{ Report *Report }

func (e *DriftError) Error() string { return i18n.Text("drift detected") }

// TamperError reports downloaded content whose dirhash differs from the lock (AC-3).
type TamperError struct {
	Name, Want, Got string
}

func (e *TamperError) Error() string {
	return i18n.Format("remote contents do not match the lock and may have been modified: %s\nlock:     %s\ncomputed: %s\nVerify that the source has not been tampered with; if it is valid, use skillmod get to lock it again", e.Name, e.Want, e.Got)
}

// NameConflictError reports the same name referring to different sources (AC-8).
type NameConflictError struct {
	Name, Existing, Incoming string
}

func (e *NameConflictError) Error() string {
	return i18n.Format("local name conflict: %q already exists (source %s); the new source is %s\nUse an alias to distinguish them: skillmod get --alias <new-name> %s", e.Name, e.Existing, e.Incoming, e.Incoming)
}

func (e *Engine) adapters() ([]install.Adapter, error) {
	return install.ByNames(e.Config.Agents)
}

func (e *Engine) loadMod() (*modfile.Mod, error) {
	m, err := modfile.LoadMod(e.Root)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("%s", i18n.Text("SKILL.mod not found\nAdvice: run skillmod init or skillmod get first"))
	}
	return m, err
}

// loadModOrEmpty returns an empty declaration when none exists so get can create it automatically.
func (e *Engine) loadModOrEmpty() (*modfile.Mod, error) {
	m, err := modfile.LoadMod(e.Root)
	if os.IsNotExist(err) {
		return &modfile.Mod{SchemaVersion: modfile.SchemaVersion}, nil
	}
	return m, err
}

func (e *Engine) loadLock() *modfile.Lock {
	l, err := modfile.LoadLock(e.Root)
	if err != nil {
		return &modfile.Lock{}
	}
	return l
}

func findLock(l *modfile.Lock, name string) *modfile.LockSkill {
	for i := range l.Skills {
		if l.Skills[i].Name == name {
			return &l.Skills[i]
		}
	}
	return nil
}

// saveLockIfChanged writes SKILL.lock only when content changes, avoiding writes when already converged (AC-2).
func (e *Engine) saveLockIfChanged(lock *modfile.Lock) error {
	newBytes, err := modfile.MarshalLock(lock)
	if err != nil {
		return err
	}
	old, err := os.ReadFile(filepath.Join(e.Root, modfile.LockFileName))
	if err == nil && bytes.Equal(old, newBytes) {
		return nil
	}
	return modfile.SaveLock(e.Root, lock)
}

func upsertLock(l *modfile.Lock, e modfile.LockSkill) {
	if existing := findLock(l, e.Name); existing != nil {
		*existing = e
		return
	}
	l.Skills = append(l.Skills, e)
}

// splitSource separates a mod entry's <repo>[//<subdir>] source field, which excludes the version.
func splitSource(src string) (repo, subdir string, err error) {
	a, err := address.Parse(src)
	if err != nil {
		return "", "", fmt.Errorf(i18n.Text("invalid source in SKILL.mod: %w"), err)
	}
	if a.Ref != "" {
		return "", "", fmt.Errorf(i18n.Text("the source field must not contain a version reference: %q"), src)
	}
	return a.Repo, a.Subdir, nil
}

// hashTree computes a snapshot's dirhash using fetch-side semantics.
func hashTree(t *source.Tree) (string, error) {
	data := make(map[string][]byte, len(t.Files))
	paths := make([]string, 0, len(t.Files))
	for _, f := range t.Files {
		data[f.Path] = f.Data
		paths = append(paths, f.Path)
	}
	return dirhash.HashBlobs(paths, func(name string) (io.ReadCloser, error) {
		b, ok := data[name]
		if !ok {
			return nil, fmt.Errorf(i18n.Text("internal error: missing blob %s"), name)
		}
		return io.NopCloser(bytes.NewReader(b)), nil
	})
}

// materialized is entry content present in the global store.
type materialized struct {
	version    string // exact complete tag or pseudo-version
	commit     string
	dirhash    string
	contentDir string
	note       string
}

type refsResult struct {
	refs *resolve.Refs
	err  error
}

type refsMemo map[string]refsResult

type snapshotKey struct {
	repo    string
	version string
}

type operationMemo struct {
	refs      refsMemo
	snapshots map[snapshotKey]*store.Snapshot
	progress  ui.Progress
}

func newOperationMemo(progress ui.Progress) *operationMemo {
	return &operationMemo{
		refs:      refsMemo{},
		snapshots: map[snapshotKey]*store.Snapshot{},
		progress:  progress,
	}
}

func (m *operationMemo) setProgress(messages ...string) {
	if m != nil && m.progress != nil {
		m.progress.Set(messages...)
	}
}

func (e *Engine) refs(ctx context.Context, repo string, memo *operationMemo) (*resolve.Refs, error) {
	key := source.RepoIdentity(repo)
	if result, ok := memo.refs[key]; ok {
		return result.refs, result.err
	}
	memo.setProgress(
		i18n.Format("checking remote versions for %s", key),
		i18n.Text("contacting the Git server"),
		i18n.Text("waiting for remote references"),
	)
	refs, err := e.Source.Refs(ctx, repo)
	memo.refs[key] = refsResult{refs: refs, err: err}
	return refs, err
}

type skillSubdirError struct {
	Subdir  string
	Version string
}

func (e *skillSubdirError) Error() string {
	return i18n.Format("skill subdirectory %q does not exist in local repository version %s", e.Subdir, e.Version)
}

func pathWithinSubdir(name, subdir string) bool {
	return subdir == "" || name == subdir || strings.HasPrefix(name, subdir+"/")
}

func snapshotSkillDir(snap *store.Snapshot, subdir string) (string, error) {
	for _, name := range snap.Info.Symlinks {
		if pathWithinSubdir(name, subdir) {
			return "", &source.SymlinkError{Path: name}
		}
	}
	for _, name := range snap.Info.Submodules {
		if pathWithinSubdir(name, subdir) {
			return "", &source.SubmoduleError{Path: name}
		}
	}
	dir := snap.ContentDir
	if subdir != "" {
		dir = filepath.Join(dir, filepath.FromSlash(subdir))
	}
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		return "", &skillSubdirError{Subdir: subdir, Version: snap.Info.Version}
	}
	return dir, nil
}

func (e *Engine) snapshot(repo, version string, memo *operationMemo) (*store.Snapshot, error) {
	key := snapshotKey{repo: source.RepoIdentity(repo), version: version}
	if snap, ok := memo.snapshots[key]; ok {
		return snap, nil
	}
	memo.setProgress(
		i18n.Format("verifying cached snapshot for %s", key.repo),
		i18n.Text("checking cached content integrity"),
		i18n.Text("reading the local repository cache"),
	)
	snap, err := e.Store.GetSnapshot(repo, version)
	if err == nil {
		memo.snapshots[key] = snap
	}
	return snap, err
}

func (e *Engine) snapshotMaterialized(repo, subdir, version, commit, wantHash string, memo *operationMemo) (*materialized, bool, error) {
	snap, err := e.snapshot(repo, version, memo)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	want := store.SnapshotInfo{Repo: repo, Version: version, Commit: commit}
	if commit != "" && snap.Info.Commit != commit {
		return nil, false, &store.SnapshotConflictError{Path: snap.ContentDir, Have: snap.Info, Want: want}
	}
	skillDir, err := snapshotSkillDir(snap, subdir)
	if err != nil {
		return nil, false, err
	}
	h := snap.Info.Treehash
	if subdir != "" {
		memo.setProgress(
			i18n.Format("verifying skill content in %s", source.RepoIdentity(repo)),
			i18n.Text("hashing the selected skill"),
			i18n.Text("checking cached content integrity"),
		)
		h, err = dirhash.HashDir(skillDir)
		if err != nil {
			return nil, false, err
		}
	}
	if wantHash != "" && h != wantHash {
		return nil, false, &TamperError{Name: subdir, Want: wantHash, Got: h}
	}
	return &materialized{
		version: snap.Info.Version, commit: snap.Info.Commit, dirhash: h,
		contentDir: skillDir,
	}, true, nil
}

// materialize ensures resolved content is stored in a persistent version snapshot and returns its location.
// A non-empty wantHash requires an exact match and reports tampering on mismatch (AC-3).
func (e *Engine) materialize(ctx context.Context, repo, subdir string, res resolve.Resolution, wantHash string, memo *operationMemo) (*materialized, error) {
	// A resolved tag can directly address an immutable snapshot by source and version.
	if res.Version != "" {
		if mat, ok, err := e.snapshotMaterialized(repo, subdir, res.Version, res.Commit, wantHash, memo); err != nil || ok {
			return mat, err
		}
	}

	// Consult the resolution index first for commits and pseudo-versions to locate their final version snapshots.
	key := res.Version
	if res.Kind == resolve.KindCommit {
		key = res.Commit
	}
	if ent, ok, err := e.Store.GetResolved(repo, subdir, key); err != nil {
		return nil, err
	} else if ok {
		if ent.Commit == res.Commit {
			if mat, found, err := e.snapshotMaterialized(repo, subdir, ent.Version, ent.Commit, wantHash, memo); err != nil || found {
				return mat, err
			}
		}
	}

	memo.setProgress(
		i18n.Format("downloading repository snapshot from %s", source.RepoIdentity(repo)),
		i18n.Text("fetching Git objects"),
		i18n.Text("reading repository contents"),
	)
	tree, err := e.Source.FetchRef(ctx, repo, res.Commit, res.FetchRef)
	if err != nil {
		return nil, err
	}
	treeHash, err := hashTree(tree)
	if err != nil {
		return nil, err
	}
	version := res.Version
	if res.Kind == resolve.KindCommit {
		version = resolve.PseudoVersion(tree.CommitTime, res.Commit)
	}
	snap, err := e.Store.PutSnapshot(store.SnapshotInfo{
		Repo: repo, Version: version, Commit: res.Commit, Treehash: treeHash,
		Symlinks: tree.Symlinks, Submodules: tree.Submodules,
	}, tree.Files)
	if err != nil {
		return nil, err
	}
	memo.snapshots[snapshotKey{repo: source.RepoIdentity(repo), version: version}] = snap
	skillDir, err := snapshotSkillDir(snap, subdir)
	if err != nil {
		return nil, err
	}
	h := snap.Info.Treehash
	if subdir != "" {
		h, err = dirhash.HashDir(skillDir)
		if err != nil {
			return nil, err
		}
	}
	if wantHash != "" && h != wantHash {
		return nil, &TamperError{Name: subdir, Want: wantHash, Got: h}
	}
	ent := store.ResolveEntry{Version: version, Commit: res.Commit, Dirhash: h}
	if err := e.Store.PutResolved(repo, subdir, version, ent); err != nil {
		return nil, err
	}
	if err := e.Store.PutResolved(repo, subdir, res.Commit, ent); err != nil {
		return nil, err
	}
	return &materialized{version, res.Commit, h, skillDir, ""}, nil
}

// resolveAndFetch resolves a ref and materializes its content, falling back to the resolution index on network failure (PRD offline hit).
func (e *Engine) resolveAndFetch(ctx context.Context, repo, subdir, ref string, memo *operationMemo) (*materialized, error) {
	// Prefer persistent resolution records and immutable snapshots for exact versions and commits.
	// update owns remote refreshes, so repeated get operations avoid the network like the Go module cache.
	if ref != "" {
		if ent, ok, indexErr := e.Store.GetResolved(repo, subdir, ref); indexErr != nil {
			return nil, indexErr
		} else if ok {
			if mat, found, snapErr := e.snapshotMaterialized(repo, subdir, ent.Version, ent.Commit, ent.Dirhash, memo); snapErr != nil {
				return nil, snapErr
			} else if found {
				mat.note = i18n.Text("from a local version snapshot; not verified online")
				return mat, nil
			}
		}

		if resolve.IsSHA(ref) {
			version, ok, lookupErr := e.Store.FindSnapshotVersionByCommit(repo, ref)
			if lookupErr != nil {
				return nil, lookupErr
			}
			if ok {
				mat, found, snapErr := e.snapshotMaterialized(repo, subdir, version, ref, "", memo)
				if snapErr != nil {
					var missing *skillSubdirError
					if !errors.As(snapErr, &missing) {
						return nil, snapErr
					}
				} else if found {
					mat.note = i18n.Text("from a local repository commit snapshot; not verified online")
					if indexErr := e.Store.PutResolved(repo, subdir, ref, store.ResolveEntry{Version: mat.version, Commit: mat.commit, Dirhash: mat.dirhash}); indexErr != nil {
						return nil, indexErr
					}
					return mat, nil
				}
			}
		}

		// Prefer full-repository snapshots for explicit semantic versions and pseudo-versions.
		// Like the Go module cache, do not access the remote when the version is already local; other skills
		// at the same repo@version can then be derived directly from snapshot subdirectories.
		if semver.IsValid(ref) {
			if mat, found, snapErr := e.snapshotMaterialized(repo, subdir, ref, "", "", memo); snapErr != nil {
				var missing *skillSubdirError
				if !errors.As(snapErr, &missing) {
					return nil, snapErr
				}
			} else if found {
				mat.note = i18n.Text("from a local repository version snapshot; not verified online")
				if indexErr := e.Store.PutResolved(repo, subdir, ref, store.ResolveEntry{Version: mat.version, Commit: mat.commit, Dirhash: mat.dirhash}); indexErr != nil {
					return nil, indexErr
				}
				return mat, nil
			}
		}

		// ls-remote returns repository-wide refs. If another subdirectory already queried the same repository,
		// an exact tag can reuse that snapshot, while latest and update still force a remote refresh.
		if !resolve.IsPseudoVersion(ref) {
			if cachedRefs, ok, _ := e.Store.GetRepoRefs(repo); ok {
				cachedRes, resolveErr := resolve.Resolve(resolve.Request{Repo: repo, Subdir: subdir, Ref: ref}, cachedRefs)
				// For a monorepo, trust only the highest-priority <subdir>/<ref> match.
				// A cached root-tag match may be stale if the remote later added a more specific subdirectory tag.
				cacheHit := resolveErr == nil && cachedRes.Kind == resolve.KindTag &&
					(subdir == "" || cachedRes.Version == subdir+"/"+ref)
				if cacheHit {
					mat, materializeErr := e.materialize(ctx, repo, subdir, *cachedRes, "", memo)
					if materializeErr != nil {
						return nil, materializeErr
					}
					if indexErr := e.Store.PutResolved(repo, subdir, ref, store.ResolveEntry{Version: mat.version, Commit: mat.commit, Dirhash: mat.dirhash}); indexErr != nil {
						return nil, indexErr
					}
					mat.note = i18n.Text("resolved using the same-repository reference cache; remote was not refreshed")
					return mat, nil
				}
			}
		}
	}

	refs, err := e.refs(ctx, repo, memo)
	if err != nil {
		// Offline fallback: use a resolution-index and version-snapshot hit.
		if ent, ok, indexErr := e.Store.GetResolved(repo, subdir, ref); indexErr != nil {
			return nil, indexErr
		} else if ok {
			if mat, found, snapErr := e.snapshotMaterialized(repo, subdir, ent.Version, ent.Commit, ent.Dirhash, memo); snapErr != nil {
				return nil, snapErr
			} else if found {
				mat.note = i18n.Text("from a local version snapshot; not verified online")
				return mat, nil
			}
		}
		return nil, fmt.Errorf(i18n.Text("network unavailable and no local version snapshot exists: %w"), err)
	}
	if err := e.Store.PutRepoRefs(repo, refs); err != nil {
		return nil, err
	}
	if resolve.IsPseudoVersion(ref) {
		// A pseudo-version is not a Git ref and cannot be resolved by ls-remote; use the index because reverse lookup by sha12 is unreliable.
		if ent, ok, indexErr := e.Store.GetResolved(repo, subdir, ref); indexErr != nil {
			return nil, indexErr
		} else if ok {
			if mat, found, snapErr := e.snapshotMaterialized(repo, subdir, ent.Version, ent.Commit, ent.Dirhash, memo); snapErr != nil {
				return nil, snapErr
			} else if found {
				return mat, nil
			}
		}
		return nil, fmt.Errorf(i18n.Text("pseudo-version %s is not in the local resolution index (cross-machine synchronization of pseudo-version entries relies on the commit field in SKILL.lock)"), ref)
	}
	res, err := resolve.Resolve(resolve.Request{Repo: repo, Subdir: subdir, Ref: ref}, refs)
	if err != nil {
		return nil, err
	}
	mat, err := e.materialize(ctx, repo, subdir, *res, "", memo)
	if err != nil {
		return nil, err
	}
	// Index the original ref so an offline get of the same ref hits directly (PRD §3.2 error table).
	if err := e.Store.PutResolved(repo, subdir, ref, store.ResolveEntry{Version: mat.version, Commit: mat.commit, Dirhash: mat.dirhash}); err != nil {
		return nil, err
	}
	return mat, nil
}

// materializeLocked materializes content from an existing authoritative lock entry (AC-10):
// it skips ls-remote and uses the lock's commit and dirhash, requiring the computed hash to match (AC-3).
func (e *Engine) materializeLocked(ctx context.Context, repo, subdir string, lk modfile.LockSkill, memo *operationMemo) (string, error) {
	if mat, ok, err := e.snapshotMaterialized(repo, subdir, lk.Version, lk.Commit, lk.Dirhash, memo); err != nil {
		return "", err
	} else if ok {
		return mat.contentDir, nil
	}
	commit := lk.Commit
	if commit == "" {
		// Fill a missing commit in an old lock through the resolution index or network.
		if ent, ok, indexErr := e.Store.GetResolved(repo, subdir, lk.Version); indexErr != nil {
			return "", indexErr
		} else if ok {
			commit = ent.Commit
		} else {
			refs, err := e.refs(ctx, repo, memo)
			if err != nil {
				return "", fmt.Errorf(i18n.Text("entry %s is not in a local version snapshot and the network is unavailable: %w"), lk.Name, err)
			}
			res, err := resolve.Resolve(resolve.Request{Repo: repo, Subdir: subdir, Ref: lk.Version}, refs)
			if err != nil {
				return "", err
			}
			commit = res.Commit
		}
	}
	fetchRef := ""
	if !resolve.IsPseudoVersion(lk.Version) && !resolve.IsSHA(lk.Version) {
		fetchRef = "refs/tags/" + lk.Version
	}
	memo.setProgress(
		i18n.Format("downloading repository snapshot from %s", source.RepoIdentity(repo)),
		i18n.Text("fetching Git objects"),
		i18n.Text("reading repository contents"),
	)
	tree, err := e.Source.FetchRef(ctx, repo, commit, fetchRef)
	if err != nil {
		return "", err
	}
	treeHash, err := hashTree(tree)
	if err != nil {
		return "", err
	}
	snap, err := e.Store.PutSnapshot(store.SnapshotInfo{
		Repo: repo, Version: lk.Version, Commit: commit, Treehash: treeHash,
		Symlinks: tree.Symlinks, Submodules: tree.Submodules,
	}, tree.Files)
	if err != nil {
		return "", err
	}
	memo.snapshots[snapshotKey{repo: source.RepoIdentity(repo), version: lk.Version}] = snap
	skillDir, err := snapshotSkillDir(snap, subdir)
	if err != nil {
		return "", err
	}
	h := snap.Info.Treehash
	if subdir != "" {
		h, err = dirhash.HashDir(skillDir)
		if err != nil {
			return "", err
		}
	}
	if h != lk.Dirhash {
		return "", &TamperError{Name: lk.Name, Want: lk.Dirhash, Got: h}
	}
	return skillDir, nil
}

// plannedInstall is one pending installation action.
type plannedInstall struct {
	name       string
	contentDir string
	targets    []string
}

// applyInstalls writes each installation atomically and rolls all of them back if installation fails.
// On success it returns finalize; callers invoke finalize(true) after writing mod and lock to remove backups,
// or finalize(false) on a write failure to restore old directories and keep declarations aligned with the filesystem.
func applyInstalls(plans []plannedInstall) (finalize func(ok bool) error, err error) {
	type applied struct {
		restore func() error
		commit  func()
	}
	var dones []applied
	rollback := func() error {
		var first error
		for i := len(dones) - 1; i >= 0; i-- {
			if err := dones[i].restore(); err != nil && first == nil {
				first = err
			}
		}
		return first
	}
	for _, p := range plans {
		for _, tgt := range p.targets {
			restore, commit, err := install.Install(p.contentDir, tgt)
			if err != nil {
				rollback()
				return nil, fmt.Errorf(i18n.Text("install %s to %s (all changes were rolled back): %w"), p.name, tgt, err)
			}
			dones = append(dones, applied{restore, commit})
		}
	}
	return func(ok bool) error {
		if !ok {
			return rollback()
		}
		for _, d := range dones {
			d.commit()
		}
		return nil
	}, nil
}
