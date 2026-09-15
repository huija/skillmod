// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

// Package cli provides the entry layer for the nine subcommands: validate arguments, call the engine, and format output.
// All business logic lives in internal/engine; this layer only handles I/O.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/huija/skillmod/internal/config"
	"github.com/huija/skillmod/internal/engine"
	"github.com/huija/skillmod/internal/i18n"
	"github.com/huija/skillmod/internal/install"
	"github.com/huija/skillmod/internal/source"
	"github.com/huija/skillmod/internal/store"
	"github.com/huija/skillmod/internal/ui"
)

// Exit-code contract.
const (
	ExitOK      = 0
	ExitError   = 1
	ExitDrift   = 2 // verify detected drift; intended for CI use
	ExitPartial = 3 // safe conflict handling skipped at least one target
)

// Version is set by main from the build-time version metadata.
var Version = "dev"

// rootOptions belongs to one command tree. Keeping flag state on the tree
// makes NewRootCmd safe to call repeatedly or concurrently in one process.
type rootOptions struct {
	installMode string
	global      bool
	json        bool
	yes         bool
	dryRun      bool
}

// NewRootCmd assembles the root command and all subcommands.
func NewRootCmd() *cobra.Command {
	options := &rootOptions{}
	root := &cobra.Command{
		Use:           "skillmod",
		Version:       Version,
		Short:         i18n.Text("cli.root.short"),
		Long:          i18n.Text("cli.root.long"),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	// The persistent flags that appear on ordinary commands carry a one-letter
	// shorthand. --install-mode deliberately has none: "m" collides with the
	// usual meaning of "message" and the flag is typed rarely, so the long
	// spelling is clearer than an ambiguous letter.
	pf := root.PersistentFlags()
	pf.StringVar(&options.installMode, "install-mode", "", i18n.Text("cli.root.flag_install_mode"))
	pf.BoolVarP(&options.global, "global", "g", false, i18n.Text("cli.root.flag_global"))
	pf.BoolVarP(&options.json, "json", "j", false, i18n.Text("cli.root.flag_json"))
	pf.BoolVarP(&options.yes, "yes", "y", false, i18n.Text("cli.root.flag_yes"))
	pf.BoolVarP(&options.dryRun, "dry-run", "n", false, i18n.Text("cli.root.flag_dry_run"))

	root.AddCommand(
		newInitCmd(options),
		newGetCmd(options),
		newSyncCmd(options),
		newListCmd(options),
		newWhyCmd(options),
		newUpdateCmd(options),
		newRemoveCmd(options),
		newPruneCmd(options),
		newShareCmd(options),
		newVerifyCmd(options),
		newUpgradeCmd(options),
	)
	return root
}

// Execute runs the root command and maps errors to exit codes.
func Execute() int {
	if err := NewRootCmd().Execute(); err != nil {
		code := exitCode(err)
		// Drift and partial completion are expected operational outcomes. Their
		// commands already emitted a human summary (or a JSON report), so only
		// unexpected failures need an additional stderr diagnostic here.
		if code == ExitError {
			fmt.Fprintln(os.Stderr, err)
		}
		return code
	}
	return ExitOK
}

func exitCode(err error) int {
	var outputErr *outputError
	if errors.As(err, &outputErr) {
		return ExitError
	}
	var drift *engine.DriftError
	if errors.As(err, &drift) {
		return ExitDrift
	}
	var partial *engine.PartialError
	if errors.As(err, &partial) {
		return ExitPartial
	}
	return ExitError
}

// newEngine selects declarations and installation roots while sharing one user store.
func (options *rootOptions) newEngine() (*engine.Engine, error) {
	var root string
	var err error
	if options.global {
		root, err = os.UserHomeDir()
	} else {
		root, err = os.Getwd()
	}
	if err != nil {
		return nil, err
	}
	s, err := store.Open(Version)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if options.installMode != "" {
		cfg.InstallMode = install.Mode(options.installMode)
	}
	if err := install.ValidateMode(cfg.InstallMode); err != nil {
		return nil, err
	}
	manifestRoot := ""
	if options.global {
		manifestRoot = filepath.Join(s.Root(), "global")
	}
	return &engine.Engine{
		ManifestRoot: manifestRoot,
		Root:         root,
		Source:       &source.Source{VCSRoot: s.VCSRoot()},
		Store:        s,
		Config:       cfg,
	}, nil
}

// newIO configures I/O channels from global flags and terminal state.
// In --json mode stdout must carry only the machine-readable report, so
// human-readable engine summaries are routed to stderr.
func (options *rootOptions) newIO(cmd *cobra.Command) engine.IO {
	out := cmd.OutOrStdout()
	if options.json {
		out = cmd.ErrOrStderr()
	}
	io := engine.IO{
		Out: out,
		Yes: options.yes,
	}
	if !options.yes && isTerminal(os.Stdin) {
		io.Confirm = ui.Interactive(os.Stdin, cmd.ErrOrStderr())
	}
	if isTerminal(os.Stderr) {
		io.Progress = ui.AnimatedProgress(cmd.ErrOrStderr())
	}
	return io
}

func (options *rootOptions) mutationOptions() engine.MutationOptions {
	return engine.MutationOptions{DryRun: options.dryRun}
}

func isTerminal(f *os.File) bool {
	// Use ioctl because /dev/null is also ModeCharDevice and a Stat-based check would misclassify it.
	return term.IsTerminal(int(f.Fd()))
}

// output renders structured JSON for --json; otherwise the engine has already printed a summary.
func (options *rootOptions) output(cmd *cobra.Command, rep *engine.Report) error {
	if !options.json || rep == nil {
		return nil
	}
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return &outputError{err: err}
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), string(data))
	if err != nil {
		return &outputError{err: err}
	}
	return nil
}

type outputError struct{ err error }

func (e *outputError) Error() string { return fmt.Sprintf("write JSON output: %v", e.err) }
func (e *outputError) Unwrap() error { return e.err }
