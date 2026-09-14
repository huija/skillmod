// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"fmt"

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

	rep := &Report{Action: CommandWhy}
	for _, skill := range m.Skills {
		if skill.Name != name && skill.DirName() != name {
			continue
		}
		locked := findLock(lock, skill)
		entry := e.inspectSkill(skill, locked, adapters)
		rep.Entries = append(rep.Entries, entry)
	}
	if len(rep.Entries) == 0 {
		return nil, fmt.Errorf(i18n.Text("engine.remove.entry_skill_mod"), name)
	}
	for _, entry := range rep.Entries {
		source := entry.Source
		if source == "" {
			source = i18n.Text("engine.why.local")
		}
		if err := io.printf(i18n.Text("engine.why.directory"), entry.Name, entry.Directory, source, entry.Version); err != nil {
			return rep, err
		}
		if entry.Commit != "" {
			if err := io.printf(i18n.Text("engine.why.commit"), entry.Commit); err != nil {
				return rep, err
			}
		}
		if entry.Dirhash != "" {
			if err := io.printf(i18n.Text("engine.why.dirhash"), entry.Dirhash); err != nil {
				return rep, err
			}
		}
		for _, target := range entry.TargetResults {
			if err := io.printf(i18n.Text("engine.why.why"), target.Path, target.Action); err != nil {
				return rep, err
			}
		}
	}
	return rep, nil
}
