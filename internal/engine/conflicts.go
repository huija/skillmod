// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package engine

import (
	"fmt"

	"github.com/huija/skillmod/internal/i18n"
)

// Conflict policies for an existing destination whose content does not match
// the incoming copy. get, sync, update, and share share one resolver so the
// interaction reads the same everywhere: "ask" resolves per conflict in an
// interactive session, skips under --yes, and aborts with the conflict list
// when no one can answer; overwrite and skip answer every conflict at once.
const (
	ConflictAsk       = "ask"
	ConflictOverwrite = "overwrite"
	ConflictSkip      = "skip"
)

// ValidateConflictPolicy rejects an unknown policy before any work starts.
func ValidateConflictPolicy(policy string) error {
	switch policy {
	case "", ConflictAsk, ConflictOverwrite, ConflictSkip:
		return nil
	default:
		return fmt.Errorf(i18n.Text("engine.conflict.unknown_policy"), policy)
	}
}

// resolveConflicts applies the policy plus interactive or non-interactive
// rules and returns the directories to skip; every conflict outside the map
// is overwritten. Commands without an --on-conflict flag pass ConflictAsk,
// which is also share's default.
func resolveConflicts(io IO, conflicts []conflict, policy string) (skip map[string]bool, err error) {
	skip = map[string]bool{}
	if len(conflicts) == 0 {
		return skip, nil
	}
	switch policy {
	case ConflictOverwrite:
		return skip, nil
	case ConflictSkip:
		for _, c := range conflicts {
			skip[c.dir] = true
		}
		return skip, nil
	}
	if io.Yes {
		for _, c := range conflicts {
			skip[c.dir] = true
			if err := io.printf(i18n.Text("engine.conflict.skipped_yes"), c.dir); err != nil {
				return nil, err
			}
		}
		return skip, nil
	}
	if io.Confirm != nil {
		for _, c := range conflicts {
			choice, err := io.Confirm.Choose(
				i18n.Format("engine.conflict.exists_mismatch", c.dir),
				[]string{
					i18n.Text("engine.conflict.overwrite"),
					i18n.Text("engine.conflict.skip"),
					i18n.Text("engine.conflict.abort"),
				})
			if err != nil {
				return nil, err
			}
			switch choice {
			case 0: // Overwrite.
			case 1:
				skip[c.dir] = true
			default:
				return nil, fmt.Errorf("%s", i18n.Text("engine.conflict.aborted"))
			}
		}
		return skip, nil
	}
	msg := i18n.Text("engine.conflict.detected")
	for _, c := range conflicts {
		msg += "\n  " + c.dir
	}
	return nil, fmt.Errorf("%s\n%s", msg, i18n.Text("engine.conflict.review"))
}
