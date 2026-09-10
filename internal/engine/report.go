// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package engine

import "github.com/huija/skillmod/internal/i18n"

// Action is a stable, machine-readable command or entry outcome.
type Action string

const (
	ActionConflict     Action = "conflict"
	ActionDrift        Action = "drift"
	ActionInstall      Action = "install"
	ActionInstalled    Action = "installed"
	ActionKeep         Action = "keep"
	ActionLocal        Action = "local"
	ActionMissing      Action = "missing"
	ActionPartial      Action = "partial"
	ActionRemove       Action = "remove"
	ActionSkip         Action = "skip"
	ActionUnlocked     Action = "unlocked"
	ActionUnverifiable Action = "unverifiable"
)

// TargetReport records one installation directory's outcome.
type TargetReport struct {
	Path   string `json:"path"`
	Action Action `json:"action"`
}

// EntryReport is one entry's result and the structured unit emitted by --json.
type EntryReport struct {
	Name          string         `json:"name"`
	Source        string         `json:"source,omitempty"`
	Action        Action         `json:"action"`
	Version       string         `json:"version,omitempty"`
	Commit        string         `json:"commit,omitempty"`
	Dirhash       string         `json:"dirhash,omitempty"`
	Directory     string         `json:"directory,omitempty"`
	Note          string         `json:"note,omitempty"`
	Targets       []string       `json:"targets,omitempty"`
	TargetResults []TargetReport `json:"targetResults,omitempty"`
}

// Report is the structured result of a command.
type Report struct {
	Action  Action        `json:"action"`
	Entries []EntryReport `json:"entries"`
	Notes   []string      `json:"notes,omitempty"`
}

// PartialError reports that at least one installation target was safely
// preserved while allowing independent work to complete.
type PartialError struct{ Report *Report }

func (e *PartialError) Error() string {
	return i18n.Text("engine.report.completed_partially")
}

func setTargetResult(entry *EntryReport, path string, action Action) {
	for i := range entry.TargetResults {
		if entry.TargetResults[i].Path == path {
			entry.TargetResults[i].Action = action
			return
		}
	}
	entry.TargetResults = append(entry.TargetResults, TargetReport{Path: path, Action: action})
}

func appendNote(existing, note string) string {
	if existing == "" {
		return note
	}
	if note == "" {
		return existing
	}
	return existing + "; " + note
}

// mergeInspectionStatus retains the most actionable aggregate status when a
// skill is installed into more than one adapter directory. It keeps results
// independent of adapter order while targetResults preserves every detail.
func mergeInspectionStatus(current, next Action) Action {
	if inspectionSeverity(next) > inspectionSeverity(current) {
		return next
	}
	return current
}

func inspectionSeverity(action Action) int {
	switch action {
	case ActionDrift, ActionUnverifiable:
		return 3
	case ActionMissing:
		return 2
	case ActionUnlocked:
		return 1
	default:
		return 0
	}
}

func partialError(report *Report, conflicts []conflict, skipped map[string]bool) error {
	for _, conflict := range conflicts {
		if skipped[conflict.dir] {
			return &PartialError{Report: report}
		}
	}
	return nil
}

func skippedConflictCount(conflicts []conflict, skipped map[string]bool) int {
	count := 0
	for _, conflict := range conflicts {
		if skipped[conflict.dir] {
			count++
		}
	}
	return count
}
