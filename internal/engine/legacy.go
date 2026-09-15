// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/huija/skillmod/internal/address"
	"github.com/huija/skillmod/internal/fsutil"
	"github.com/huija/skillmod/internal/i18n"
	"github.com/huija/skillmod/internal/modfile"
	repoaddr "github.com/huija/skillmod/internal/repo"
	"github.com/huija/skillmod/internal/resolve"
	"github.com/huija/skillmod/internal/source"
)

// legacySkill reads the upstream skills CLI's v3 global and v1 project records.
// computedHash (project SHA-256) deliberately is not treated as a Git object ID.
// Actual installed bytes are always compared using skillmod's portable dirhash.
type legacySkill struct {
	Source          string `json:"source"`
	SourceType      string `json:"sourceType"`
	SourceURL       string `json:"sourceUrl"`
	SkillPath       string `json:"skillPath"`
	Ref             string `json:"ref"`
	SkillFolderHash string `json:"skillFolderHash"`
}

func (e *Engine) legacySkills() (map[string]legacySkill, error) {
	version := 1
	paths := []string{filepath.Join(e.Root, "skills-lock.json")}
	if e.ManifestRoot != "" {
		version = 3
		paths = []string{filepath.Join(e.Root, ".agents", ".skill-lock.json")}
		if state := os.Getenv("XDG_STATE_HOME"); state != "" {
			if !filepath.IsAbs(state) {
				return nil, fmt.Errorf(i18n.Text("engine.legacy.absolute_path"), "XDG_STATE_HOME", state)
			}
			// Newer versions of the upstream installer use the XDG state path;
			// retain the .agents fallback for existing installations and for
			// platforms that do not set XDG_STATE_HOME.
			paths = append([]string{filepath.Join(state, "skills", ".skill-lock.json")}, paths...)
		}
	}
	var data []byte
	var filename string
	for _, candidate := range paths {
		candidateData, err := os.ReadFile(candidate)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		data, filename = candidateData, candidate
		break
	}
	if data == nil {
		return nil, nil
	}
	var lock struct {
		Version int                    `json:"version"`
		Skills  map[string]legacySkill `json:"skills"`
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, fmt.Errorf(i18n.Text("engine.legacy.cannot_read_previous_installer"), filename, err)
	}
	if lock.Version != version || lock.Skills == nil {
		// An unknown lock belongs to another installer generation. Ignore it
		// rather than preventing init from generating a manifest from the files
		// that are actually installed.
		return nil, nil
	}
	return lock.Skills, nil
}

func (sk legacySkill) location() (repo, subdir string, err error) {
	switch sk.SourceType {
	case "github", "git", "gitlab":
	default:
		return "", "", fmt.Errorf(i18n.Text("engine.legacy.previous_installer_source_type"), sk.SourceType)
	}
	raw := sk.SourceURL
	if raw == "" {
		raw = sk.Source
		if sk.SourceType == "github" {
			raw = "https://github.com/" + raw
		}
	}
	if raw == "" || strings.ContainsAny(raw, "\r\n\x00") {
		return "", "", fmt.Errorf(i18n.Text("engine.legacy.invalid_previous_installer"), raw)
	}
	a, err := address.Parse(raw)
	if err != nil {
		return "", "", err
	}
	if a.Ref != "" || a.Subdir != "" {
		return "", "", fmt.Errorf(i18n.Text("engine.legacy.invalid_previous_installer"), raw)
	}
	// Only native Git transports are accepted, never executable remote helpers.
	transport := a.Repo
	if !strings.Contains(transport, "://") {
		transport = repoaddr.Identity(transport) // scp-style SSH
	}
	u, err := url.Parse(transport)
	if err != nil || !repoaddr.SupportedTransport(u.Scheme) {
		return "", "", fmt.Errorf(i18n.Text("engine.legacy.invalid_previous_installer"), raw)
	}
	// Persisted Git paths are slash-separated even when imported on another OS.
	subdir = strings.ReplaceAll(sk.SkillPath, "\\", "/")
	if subdir != "" {
		if err := fsutil.ValidPath(subdir); err != nil {
			return "", "", err
		}
		if path.Base(subdir) == "SKILL.md" {
			subdir = path.Dir(subdir)
			if subdir == "." {
				subdir = ""
			}
		}
		if strings.Contains(subdir, "@") {
			return "", "", fmt.Errorf(i18n.Text("address.subdirectory_at_sign"), subdir)
		}
	}
	if sk.SkillFolderHash != "" && !resolve.IsSHA(sk.SkillFolderHash) {
		return "", "", fmt.Errorf(i18n.Text("engine.legacy.invalid_legacy_git_tree"), sk.SkillFolderHash)
	}
	return a.Repo, subdir, nil
}

type legacyImporter struct {
	engine    *Engine
	records   map[string]legacySkill // only currently installed entries
	ambiguous map[string]string      // map installation directories with conflicting legacy records to a diagnostic
	memo      *operationMemo
}

func (im *legacyImporter) match(ctx context.Context, old legacySkill, name, alias, installedHash string) (*modfile.ModSkill, *modfile.LockSkill, error) {
	repo, subdir, err := old.location()
	if err != nil {
		return nil, nil, err
	}
	res, err := im.immutableResolution(ctx, repo, subdir, old.Ref)
	if err != nil {
		return nil, nil, err
	}
	if old.SkillFolderHash != "" && old.SkillPath == "" {
		return nil, nil, fmt.Errorf("%s", i18n.Text("engine.legacy.previous_installer_recorded"))
	}
	if old.SkillPath == "" {
		root, err := im.engine.materialize(ctx, repo, "", res, "", im.memo)
		if err != nil {
			return nil, nil, err
		}
		candidates, err := skillCandidates(root.contentDir)
		if err != nil {
			return nil, nil, err
		}
		var matches []skillCandidate
		for _, candidate := range candidates {
			if candidate.name == name {
				matches = append(matches, candidate)
			}
		}
		if len(matches) != 1 {
			return nil, nil, fmt.Errorf(i18n.Text("engine.legacy.no_unique_repository_path"), name)
		}
		subdir = matches[0].subdir
	}
	if old.SkillFolderHash != "" {
		treeHash, err := im.engine.Source.TreeHash(ctx, repo, res.Commit, res.FetchRef, subdir)
		if err != nil {
			return nil, nil, err
		}
		if treeHash != old.SkillFolderHash {
			return nil, nil, fmt.Errorf(i18n.Text("engine.legacy.recorded_tree_match_repository"), old.SkillFolderHash, old.Ref, treeHash)
		}
	}
	mat, err := im.engine.materialize(ctx, repo, subdir, res, "", im.memo)
	if err != nil {
		return nil, nil, err
	}
	remoteName, err := source.SkillNameFromDir(mat.contentDir)
	if err != nil {
		return nil, nil, err
	}
	if remoteName != name {
		return nil, nil, fmt.Errorf(i18n.Text("engine.legacy.recorded_source_path_contains"), remoteName, name)
	}
	if old.Ref == "" && mat.dirhash != installedHash {
		return nil, nil, errors.New(i18n.Text("engine.legacy.record_without_revision"))
	}
	// The old installer record is authoritative for provenance. The installed
	// directory may have been edited after installation (or normalized by the
	// old installer), so a hash mismatch is reported by init but does not erase
	// the recoverable source declaration. The lock records the source revision's
	// hash; a subsequent verify reports drift and sync can align the directory.
	src, err := repoaddr.PersistedTransport(repo)
	if err != nil {
		return nil, nil, err
	}
	if subdir != "" {
		src += subdirSuffix(subdir)
	}
	sk := &modfile.ModSkill{Name: name, Alias: alias, Source: src, Version: mat.version}
	lk := &modfile.LockSkill{Name: name, Dir: alias, Source: src, Version: mat.version, Commit: mat.commit, Dirhash: mat.dirhash}
	return sk, lk, nil
}

func (im *legacyImporter) immutableResolution(ctx context.Context, repo, subdir, ref string) (resolve.Resolution, error) {
	switch {
	case ref == "":
		refs, err := im.engine.refs(ctx, repo, im.memo)
		if err != nil {
			return resolve.Resolution{}, err
		}
		res, err := resolve.Resolve(resolve.Request{Repo: repo, Subdir: subdir}, refs)
		if err != nil {
			return resolve.Resolution{}, err
		}
		return *res, nil
	case strings.HasPrefix(ref, "refs/heads/"):
		return resolve.Resolution{}, &resolve.BranchError{Ref: strings.TrimPrefix(ref, "refs/heads/")}
	case resolve.IsSHA(ref):
		return resolve.Resolution{Kind: resolve.KindCommit, Commit: ref}, nil
	}
	refs, err := im.engine.refs(ctx, repo, im.memo)
	if err != nil {
		return resolve.Resolution{}, err
	}
	res, err := resolve.Resolve(resolve.Request{Repo: repo, Subdir: subdir, Ref: strings.TrimPrefix(ref, "refs/tags/")}, refs)
	if err != nil {
		return resolve.Resolution{}, err
	}
	return *res, nil
}
