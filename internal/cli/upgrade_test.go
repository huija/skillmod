// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/huija/skillmod/internal/engine"
	"github.com/huija/skillmod/internal/upgrade"
)

// stubUpgrade replaces the release install with a scripted result, so the
// command's flag wiring, report, and exit codes are tested without GitHub.
func stubUpgrade(t *testing.T, result upgrade.Result, err error) *upgrade.Options {
	t.Helper()
	original := runUpgrade
	t.Cleanup(func() { runUpgrade = original })
	got := upgrade.Options{}
	runUpgrade = func(_ context.Context, options upgrade.Options) (upgrade.Result, error) {
		got = options
		return result, err
	}
	return &got
}

func executeUpgrade(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := NewRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

func TestUpgradeCommandInstallsAndReports(t *testing.T) {
	isolateCLI(t)
	options := stubUpgrade(t, upgrade.Result{
		Status: upgrade.StatusAvailable, Latest: "v9.9.9",
		Asset: "skillmod_9.9.9_linux_amd64.tar.gz", Updated: true,
	}, nil)

	out, _, err := executeUpgrade(t, "upgrade", "--tag", "v9.9.9")
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	if options.Tag != "v9.9.9" || options.Check || options.DryRun {
		t.Errorf("options = %+v, want the --tag release installed now", options)
	}
	if options.Current != Version {
		t.Errorf("options.Current = %q, want the running version %q", options.Current, Version)
	}
	if options.Target == "" {
		t.Error("options.Target is empty; the command must name the running executable")
	}
	if !strings.Contains(out, "upgraded skillmod from") {
		t.Errorf("output = %q, want the upgrade summary", out)
	}
}

func TestUpgradeCommandCheckIsReadOnly(t *testing.T) {
	isolateCLI(t)
	options := stubUpgrade(t, upgrade.Result{
		Status: upgrade.StatusAvailable, Latest: "v9.9.9",
	}, nil)

	out, _, err := executeUpgrade(t, "upgrade", "--check")
	if err != nil {
		t.Fatalf("upgrade --check: %v", err)
	}
	if !options.Check || options.DryRun {
		t.Errorf("options = %+v, want a check-only request", options)
	}
	if !strings.Contains(out, "v9.9.9 is available") {
		t.Errorf("output = %q, want the availability notice", out)
	}
}

// A job watches for a release by branching on the entry action, never on the
// localized note: update means a newer release exists, keep means the running
// executable is the requested one. The target action says what happened to the
// file, so a check is distinguishable from an install.
func TestUpgradeCommandJSONReportSeparatesAvailabilityFromInstall(t *testing.T) {
	tests := []struct {
		name       string
		result     upgrade.Result
		args       []string
		runErr     error
		wantEntry  engine.EntryStatus
		wantTarget engine.TargetStatus
	}{
		{
			name:       "newer release available",
			result:     upgrade.Result{Status: upgrade.StatusAvailable, Latest: "v9.9.9"},
			wantEntry:  engine.ActionUpdate,
			wantTarget: engine.ActionKeep,
		},
		{
			name:       "check reports availability without touching the file",
			result:     upgrade.Result{Status: upgrade.StatusAvailable, Latest: "v9.9.9"},
			args:       []string{"--check"},
			wantEntry:  engine.ActionUpdate,
			wantTarget: engine.ActionKeep,
		},
		{
			name:       "dry run plans the install it verified",
			result:     upgrade.Result{Status: upgrade.StatusAvailable, Latest: "v9.9.9", Asset: "skillmod_9.9.9_linux_amd64.tar.gz"},
			args:       []string{"--dry-run"},
			wantEntry:  engine.ActionUpdate,
			wantTarget: engine.ActionInstall,
		},
		{
			name:       "installed",
			result:     upgrade.Result{Status: upgrade.StatusAvailable, Latest: "v9.9.9", Updated: true},
			wantEntry:  engine.ActionUpdate,
			wantTarget: engine.ActionInstalled,
		},
		{
			name:       "already current",
			result:     upgrade.Result{Status: upgrade.StatusCurrent, Latest: "v0.0.1"},
			wantEntry:  engine.ActionKeep,
			wantTarget: engine.ActionKeep,
		},
		{
			// A failed install left the executable alone, so it must not claim the
			// install a dry run would have planned.
			name:       "failed dry run",
			result:     upgrade.Result{Status: upgrade.StatusAvailable, Latest: "v9.9.9"},
			args:       []string{"--dry-run"},
			runErr:     errors.New("checksum mismatch"),
			wantEntry:  engine.ActionUpdate,
			wantTarget: engine.ActionKeep,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateCLI(t)
			stubUpgrade(t, tt.result, tt.runErr)
			args := append([]string{"--json", "upgrade"}, tt.args...)

			out, _, _ := executeUpgrade(t, args...)
			var report engine.Report
			if err := json.Unmarshal([]byte(out), &report); err != nil {
				t.Fatalf("decode JSON report %q: %v", out, err)
			}
			if report.Action != engine.CommandUpgrade || len(report.Entries) != 1 {
				t.Fatalf("report = %+v, want one upgrade entry", report)
			}
			entry := report.Entries[0]
			if entry.Name != "skillmod" || entry.Version != tt.result.Latest {
				t.Errorf("entry = %+v, want the skillmod executable at %q", entry, tt.result.Latest)
			}
			if entry.Action != tt.wantEntry {
				t.Errorf("entry action = %q, want %q", entry.Action, tt.wantEntry)
			}
			if len(entry.TargetResults) != 1 || entry.TargetResults[0].Action != tt.wantTarget {
				t.Errorf("target results = %+v, want %q for the executable", entry.TargetResults, tt.wantTarget)
			}
		})
	}
}

// A failed upgrade still emits the report, so a job parsing --json decodes one
// document instead of empty output.
func TestUpgradeCommandJSONReportOnFailure(t *testing.T) {
	isolateCLI(t)
	stubUpgrade(t, upgrade.Result{Status: upgrade.StatusAvailable, Latest: "v9.9.9"}, errors.New("checksum mismatch"))

	out, _, err := executeUpgrade(t, "--json", "upgrade")
	if err == nil {
		t.Fatal("upgrade reported success after a failed install")
	}
	var report engine.Report
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("decode JSON report %q: %v", out, err)
	}
	if report.Action != engine.CommandUpgrade || len(report.Entries) != 1 {
		t.Fatalf("report = %+v, want one upgrade entry", report)
	}
	if got := report.Entries[0].Action; got != engine.ActionUpdate {
		t.Errorf("entry action = %q, want the available upgrade reported as update", got)
	}
}

func TestUpgradeCommandDryRunPlansTheInstall(t *testing.T) {
	isolateCLI(t)
	stubUpgrade(t, upgrade.Result{
		Status: upgrade.StatusAvailable, Latest: "v9.9.9",
		Asset: "skillmod_9.9.9_linux_amd64.tar.gz",
	}, nil)

	out, _, err := executeUpgrade(t, "upgrade", "--dry-run")
	if err != nil {
		t.Fatalf("upgrade --dry-run: %v", err)
	}
	if !strings.Contains(out, "dry-run: would install skillmod v9.9.9") {
		t.Errorf("output = %q, want the dry-run plan", out)
	}
}

func TestUpgradeCommandCurrentVersionIsSuccess(t *testing.T) {
	isolateCLI(t)
	stubUpgrade(t, upgrade.Result{Status: upgrade.StatusCurrent, Current: Version, Latest: "v0.0.1"}, nil)

	out, _, err := executeUpgrade(t, "upgrade")
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	if !strings.Contains(out, "already up to date") {
		t.Errorf("output = %q, want the up-to-date notice", out)
	}
}

// TestUpgradeCommandFailureExitsNonZero keeps the CI contract: a failed upgrade
// is an operational error, not a silent success.
func TestUpgradeCommandFailureExitsNonZero(t *testing.T) {
	isolateCLI(t)
	stubUpgrade(t, upgrade.Result{}, errors.New("checksum mismatch"))

	_, _, err := executeUpgrade(t, "upgrade")
	if err == nil {
		t.Fatal("upgrade reported success after a failed install")
	}
	if code := exitCode(err); code != ExitError {
		t.Errorf("exit code = %d, want %d", code, ExitError)
	}
}

func TestUpgradeCommandRejectsArguments(t *testing.T) {
	isolateCLI(t)
	stubUpgrade(t, upgrade.Result{}, nil)
	if _, _, err := executeUpgrade(t, "upgrade", "unexpected"); err == nil {
		t.Error("upgrade accepted a positional argument")
	}
}

// The upgrade command must not be affected by the persistent flags that select
// how skills are installed into a project.
func TestUpgradeCommandIgnoresSkillFlags(t *testing.T) {
	isolateCLI(t)
	options := stubUpgrade(t, upgrade.Result{Status: upgrade.StatusAvailable, Latest: "v9.9.9"}, nil)
	if _, _, err := executeUpgrade(t, "upgrade", "--install-mode", "copy", "-y", "-g"); err != nil {
		t.Fatalf("upgrade with unrelated persistent flags: %v", err)
	}
	if options.Target == "" {
		t.Error("options.Target is empty")
	}
}
