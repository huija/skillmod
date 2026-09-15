// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/huija/skillmod/internal/engine"
	"github.com/huija/skillmod/internal/i18n"
	"github.com/huija/skillmod/internal/upgrade"
)

// runUpgrade is a package variable so tests can drive the command without
// contacting GitHub. Every real invocation uses upgrade.Run.
var runUpgrade = upgrade.Run

func newUpgradeCmd(options *rootOptions) *cobra.Command {
	var tag string
	var check bool
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: i18n.Text("cli.upgrade.short"),
		Long:  i18n.Text("cli.upgrade.long"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return options.upgrade(cmd, tag, check)
		},
	}
	cmd.Flags().StringVarP(&tag, "tag", "t", "", i18n.Text("cli.upgrade.flag_tag"))
	cmd.Flags().BoolVarP(&check, "check", "c", false, i18n.Text("cli.upgrade.flag_check"))
	return cmd
}

// upgrade replaces the running executable. It reports through the same report
// and exit-code contract as the skill commands so automation reads one
// contract, even though the executable is not a skill.
func (options *rootOptions) upgrade(cmd *cobra.Command, tag string, check bool) error {
	target, err := upgrade.ExecutablePath()
	if err != nil {
		return err
	}
	io := options.newIO(cmd)
	result, err := runUpgrade(cmd.Context(), upgrade.Options{
		Tag:      tag,
		Check:    check,
		DryRun:   options.dryRun,
		Current:  Version,
		Target:   target,
		Progress: io.Progress,
	})
	// The JSON report is written on every path, including a failed install, so a
	// job that parses --json always decodes one document. Only the human summary
	// is skipped on failure, where Execute reports the error instead.
	rep := upgradeReport(result, target, options.dryRun, err != nil)
	if err != nil {
		return errors.Join(err, options.output(cmd, rep))
	}
	return errors.Join(options.output(cmd, rep), reportUpgrade(rep, io, result, options.dryRun))
}

// upgradeReport renders one upgrade attempt as a report entry. The action
// vocabulary is shared with the skill commands: update means an upgrade exists
// or was applied, keep means the executable already matches the requested
// release. The target's own action records what happened to the file, which is
// what tells a check, a dry run, and an install apart.
func upgradeReport(result upgrade.Result, target string, dryRun, failed bool) *engine.Report {
	rep := &engine.Report{Action: engine.CommandUpgrade}
	if result.Latest == "" {
		// The release was never resolved, so there is no version to name.
		return rep
	}
	entry := engine.EntryReport{
		Name:    "skillmod",
		Source:  upgrade.Repository,
		Version: result.Latest,
		Action:  engine.ActionKeep,
	}
	targetAction := engine.TargetStatus(engine.ActionKeep)
	switch {
	case result.Updated:
		entry.Action = engine.ActionUpdate
		targetAction = engine.ActionInstalled
	case result.Status == upgrade.StatusAvailable:
		// A newer release exists and this run did not install it: a check stopped
		// before downloading, and a dry run or a failed install left the
		// executable alone. Only a dry run reports the install it verified, so the
		// target action distinguishes the three.
		entry.Action = engine.ActionUpdate
		if dryRun && !failed {
			targetAction = engine.ActionInstall
			entry.Note = i18n.Text("engine.dry_run_files_written")
		} else {
			entry.Note = i18n.Text("cli.upgrade.note_run_upgrade")
		}
	}
	entry.TargetResults = []engine.TargetReport{{Path: target, Action: targetAction}}
	rep.Entries = append(rep.Entries, entry)
	return rep
}

// reportUpgrade prints the human summary. It runs after the JSON report so
// --json keeps stdout machine-readable.
func reportUpgrade(rep *engine.Report, io engine.IO, result upgrade.Result, dryRun bool) error {
	if io.Out == nil || len(rep.Entries) == 0 {
		return nil
	}
	target := rep.Entries[0].TargetResults[0].Path
	var line string
	switch {
	case result.Status == upgrade.StatusCurrent:
		line = i18n.Format("cli.upgrade.already_current", Version)
	case result.Updated:
		line = i18n.Format("cli.upgrade.installed", Version, result.Latest, target)
	case dryRun:
		line = i18n.Format("cli.upgrade.dry_run", result.Latest, target)
	default:
		line = i18n.Format("cli.upgrade.available", result.Latest)
	}
	if _, err := fmt.Fprintln(io.Out, line); err != nil {
		return &outputError{err: err}
	}
	return nil
}
