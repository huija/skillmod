// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package engine

import (
	"errors"
	"io/fs"

	"github.com/huija/skillmod/internal/dirhash"
	"github.com/huija/skillmod/internal/i18n"
	"github.com/huija/skillmod/internal/modfile"
)

// inspectSkill applies the same filesystem classification to list, verify, and
// why so those commands cannot disagree about missing or unreadable targets.
func (e *Engine) inspectSkill(sk modfile.ModSkill, locked *modfile.LockSkill) EntryReport {
	entry := EntryReport{
		Name: sk.Name, Source: sk.Source, Local: sk.Local, Version: sk.Version,
		Directory: sk.DirName(), Action: ActionInstalled,
	}
	if !sk.Local {
		requestedVersion := sk.Version
		entry.RequestedVersion = &requestedVersion
	}
	if locked == nil {
		entry.Action = ActionUnlocked
	} else {
		entry.Version = locked.Version
		entry.Commit = locked.Commit
		entry.Dirhash = locked.Dirhash
		if !sk.Local && sk.Version == "" {
			entry.Note = i18n.Format("engine.inspect.latest_lock", locked.Version)
		} else if !sk.Local && sk.Version != locked.Version {
			entry.Note = i18n.Format("engine.inspect.version_mismatch", sk.Version, locked.Version)
		}
	}
	target := inspectTarget(e.skillDir(sk.DirName()), locked)
	entry.TargetResults = append(entry.TargetResults, target)
	entry.Action = mergeInspectionStatus(entry.Action, target.Action)
	return entry
}

func inspectTarget(path string, locked *modfile.LockSkill) TargetReport {
	report := TargetReport{Path: path}
	hash, err := dirhash.HashDir(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		report.Action = ActionMissing
	case err != nil:
		report.Action = ActionUnverifiable
		report.Note = err.Error()
	case locked == nil:
		report.Action = ActionUnlocked
	case hash != locked.Dirhash:
		report.Action = ActionDrift
	default:
		report.Action = ActionInstalled
	}
	return report
}
