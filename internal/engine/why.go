// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/huija/skillmod/internal/dirhash"
	"github.com/huija/skillmod/internal/i18n"
)

// Why explains declarations, immutable provenance, and installation status for
// entries selected by published name or installation alias.
func (e *Engine) Why(_ context.Context, name string, io IO) (*Report, error) {
	unlock, err := e.lockState()
	if err != nil {
		return nil, err
	}
	defer unlock()
	m, err := e.loadMod()
	if err != nil {
		return nil, err
	}
	lock, err := e.loadLock()
	if err != nil {
		return nil, err
	}
	adapters, err := e.adapters()
	if err != nil {
		return nil, err
	}

	rep := &Report{Action: "why"}
	for _, skill := range m.Skills {
		if skill.Name != name && skill.DirName() != name {
			continue
		}
		entry := EntryReport{
			Name: skill.Name, Source: skill.Source, Version: skill.Version,
			Directory: skill.DirName(), Action: ActionUnlocked,
		}
		locked := findLock(lock, skill)
		if locked != nil {
			entry.Commit = locked.Commit
			entry.Dirhash = locked.Dirhash
		}
		if skill.Local {
			entry.Action = ActionLocal
		} else if locked != nil {
			entry.Action = ActionInstalled
		}
		for _, adapter := range adapters {
			dst := adapterDir(adapter, e.Root, skill.DirName())
			entry.Targets = append(entry.Targets, dst)
			hash, hashErr := dirhash.HashDir(dst)
			switch {
			case errors.Is(hashErr, fs.ErrNotExist):
				setTargetResult(&entry, dst, ActionMissing)
				entry.Action = mergeInspectionStatus(entry.Action, ActionMissing)
			case hashErr != nil:
				setTargetResult(&entry, dst, ActionUnverifiable)
				entry.Action = mergeInspectionStatus(entry.Action, ActionDrift)
			case locked == nil:
				setTargetResult(&entry, dst, ActionUnlocked)
				if !skill.Local {
					entry.Action = mergeInspectionStatus(entry.Action, ActionUnlocked)
				}
			case hash != locked.Dirhash:
				setTargetResult(&entry, dst, ActionDrift)
				entry.Action = mergeInspectionStatus(entry.Action, ActionDrift)
			default:
				setTargetResult(&entry, dst, ActionInstalled)
			}
		}
		rep.Entries = append(rep.Entries, entry)
	}
	if len(rep.Entries) == 0 {
		return nil, fmt.Errorf(i18n.Text("entry %q is not in SKILL.mod"), name)
	}
	for _, entry := range rep.Entries {
		source := entry.Source
		if source == "" {
			source = i18n.Text("local")
		}
		io.printf(i18n.Text("%s (directory %s): %s %s"), entry.Name, entry.Directory, source, entry.Version)
		if entry.Commit != "" {
			io.printf(i18n.Text("  commit: %s"), entry.Commit)
		}
		if entry.Dirhash != "" {
			io.printf(i18n.Text("  dirhash: %s"), entry.Dirhash)
		}
		for _, target := range entry.TargetResults {
			io.printf(i18n.Text("  %s: %s"), target.Path, target.Action)
		}
	}
	return rep, nil
}
