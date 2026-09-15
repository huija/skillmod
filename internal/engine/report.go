// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package engine

import "github.com/huija/skillmod/internal/i18n"

// Command is the stable machine-readable command that produced a report.
type Command string

// EntryStatus is the stable machine-readable outcome for one declaration.
type EntryStatus string

// TargetStatus is the stable machine-readable outcome for one installation directory.
type TargetStatus string

const (
	CommandGet    Command = "get"
	CommandInit   Command = "init"
	CommandList   Command = "list"
	CommandPrune  Command = "prune"
	CommandRemove Command = "remove"
	CommandShare  Command = "share"
	CommandSync   Command = "sync"
	CommandUpdate Command = "update"
	CommandVerify Command = "verify"
	CommandWhy    Command = "why"

	ActionConflict     = "conflict"
	ActionDrift        = "drift"
	ActionInstall      = "install"
	ActionInstalled    = "installed"
	ActionKeep         = "keep"
	ActionLocal        = "local"
	ActionLocalDrift   = "local-drift"
	ActionMatched      = "matched"
	ActionMissing      = "missing"
	ActionPartial      = "partial"
	ActionPrune        = "prune"
	ActionRemove       = "remove"
	ActionSkip         = "skip"
	ActionStale        = "stale"
	ActionUnlocked     = "unlocked"
	ActionUnresolved   = "unresolved"
	ActionUnverifiable = "unverifiable"
	ActionUpdate       = "update"
)

// TargetReport records one installation directory's outcome.
type TargetReport struct {
	Path   string       `json:"path"`
	Action TargetStatus `json:"action"`
	Note   string       `json:"note,omitempty"`
}

// EntryReport is one entry's result and the structured unit emitted by --json.
type EntryReport struct {
	Name    string      `json:"name"`
	Source  string      `json:"source,omitempty"`
	Local   bool        `json:"local,omitempty"`
	Action  EntryStatus `json:"action"`
	Version string      `json:"version,omitempty"`
	// RequestedVersion is present for remote inspection entries and contains
	// the exact SKILL.mod value. An empty value means the entry tracks latest.
	RequestedVersion *string        `json:"requestedVersion,omitempty"`
	Commit           string         `json:"commit,omitempty"`
	Dirhash          string         `json:"dirhash,omitempty"`
	Directory        string         `json:"directory,omitempty"`
	Note             string         `json:"note,omitempty"`
	TargetResults    []TargetReport `json:"targetResults,omitempty"`
}

// Report is the structured result of a command.
type Report struct {
	Action  Command       `json:"action"`
	Entries []EntryReport `json:"entries"`
	Notes   []string      `json:"notes,omitempty"`
}

// PartialError reports that at least one installation target was safely
// preserved while allowing independent work to complete.
type PartialError struct{ Report *Report }

func (e *PartialError) Error() string {
	return i18n.Text("engine.report.completed_partially")
}

func setTargetResult(entry *EntryReport, path string, action TargetStatus) {
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

func printReportNotes(rep *Report, io IO) error {
	for _, note := range rep.Notes {
		if err := io.printf("%s", note); err != nil {
			return err
		}
	}
	return nil
}

// mergeInspectionStatus folds a target's outcome into the entry's aggregate
// status, keeping the most actionable result while targetResults preserves the
// per-directory detail.
func mergeInspectionStatus(current EntryStatus, next TargetStatus) EntryStatus {
	candidate := EntryStatus(next)
	if inspectionSeverity(candidate) > inspectionSeverity(current) {
		return candidate
	}
	return current
}

func inspectionSeverity(action EntryStatus) int {
	switch action {
	case ActionUnverifiable:
		return 4
	case ActionDrift, ActionLocalDrift:
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
