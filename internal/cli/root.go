// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

// Package cli provides the entry layer for the subcommands: validate arguments, call the engine, and format output.
// All business logic lives in internal/engine; this layer only handles I/O.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	root, _ := newRootCmd()
	return root
}

// newRootCmd assembles the command tree and returns the flag state it owns, so
// the entry point can tell a --json run from a plain one when a handler fails
// before it reaches output.
func newRootCmd() (*cobra.Command, *rootOptions) {
	options := &rootOptions{}
	root := &cobra.Command{
		Use:           "skillmod",
		Version:       Version,
		Short:         i18n.Text("cli.root.short"),
		Long:          i18n.Text("cli.root.long"),
		SilenceUsage:  true,
		SilenceErrors: true,
		// Cobra parses the flags and resolves the subcommand before this runs,
		// so recording the command here is what separates a failure inside a
		// handler from one raised while parsing flags.
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			handlerCmd = cmd
		},
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
	return root, options
}

// Execute runs the root command and maps errors to exit codes.
func Execute() int {
	root, options := newRootCmd()
	return execute(root, options)
}

// execute runs one command tree. It is separate from Execute so a test can
// point the tree at its own buffer and still exercise the failure paths.
func execute(root *cobra.Command, options *rootOptions) int {
	jsonEmitted = false
	handlerCmd = nil
	if err := root.Execute(); err != nil {
		code := exitCode(err)
		// Drift and partial completion are expected operational outcomes. Their
		// commands already emitted a report — a JSON document under --json, a
		// human summary otherwise — so only unexpected failures need an
		// additional stderr diagnostic here.
		if code == ExitError && !jsonEmitted {
			// A handler that returns before reaching output — newEngine
			// failing is the case — still owes --json its single document, so
			// the same shape is written from here. A flag error reaches no
			// handler and keeps the plain stderr text its contract documents.
			if options.json && handlerCmd != nil {
				if writeErr := options.output(handlerCmd, nil, err); writeErr != nil {
					fmt.Fprintln(os.Stderr, writeErr)
				}
			}
			if !jsonEmitted {
				fmt.Fprintln(os.Stderr, err)
			}
		}
		return code
	}
	return ExitOK
}

// jsonEmitted records whether the current run already wrote its JSON document
// to stdout. It is reset per Execute call and set by output; a flag-parse or
// argument error, which no command handler reaches, stays plain text on
// stderr because no document was written.
//
// Known limitation: this is package-level state, so concurrent Execute calls
// in one process would race on it. The binary calls Execute exactly once per
// process, which is the only supported pattern; threading the flag through
// RunE's error return would couple every command to it for no gain.
var jsonEmitted bool

// handlerCmd records the command cobra actually reached, which separates a
// failure raised inside a command handler from one cobra raised while parsing
// flags. Only the former owes --json a document. It is reset per Execute call
// and carries the same single-call limitation as jsonEmitted.
var handlerCmd *cobra.Command

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
// In --json mode stdout carries only the machine-readable report, so every
// human-readable channel is closed: engine summaries go nowhere and the
// animated progress has no writer. Prompts still reach the terminal through
// stderr, because a person who asked for JSON in a terminal still has to
// answer them.
func (options *rootOptions) newIO(cmd *cobra.Command) engine.IO {
	channels := engine.IO{
		Out: cmd.OutOrStdout(),
		Yes: options.yes,
	}
	if options.json {
		channels.Out = io.Discard
	}
	if isTerminal(os.Stdin) {
		channels.Confirm = ui.Interactive(os.Stdin, cmd.ErrOrStderr())
	}
	if !options.json && isTerminal(os.Stderr) {
		channels.Progress = ui.AnimatedProgress(cmd.ErrOrStderr())
	}
	return channels
}

func (options *rootOptions) mutationOptions() engine.MutationOptions {
	return engine.MutationOptions{DryRun: options.dryRun}
}

func isTerminal(f *os.File) bool {
	// Use ioctl because /dev/null is also ModeCharDevice and a Stat-based check would misclassify it.
	return term.IsTerminal(int(f.Fd()))
}

// output renders structured JSON for --json; otherwise the engine has already
// printed a human summary. A failing run still emits exactly one document: the
// report describes whatever completed, and the error names why the command
// stopped, so a job that parses --json never faces an empty stdout.
func (options *rootOptions) output(cmd *cobra.Command, rep *engine.Report, err error) error {
	if !options.json || rep == nil && err == nil {
		return nil
	}
	if rep == nil {
		rep = &engine.Report{Action: engine.Command(cmd.Name())}
	}
	if err != nil {
		rep.Error = err.Error()
	}
	data, marshalErr := json.MarshalIndent(rep, "", "  ")
	if marshalErr != nil {
		return &outputError{err: marshalErr}
	}
	if _, writeErr := fmt.Fprintln(cmd.OutOrStdout(), string(data)); writeErr != nil {
		return &outputError{err: writeErr}
	}
	jsonEmitted = true
	return nil
}

type outputError struct{ err error }

func (e *outputError) Error() string { return fmt.Sprintf("write JSON output: %v", e.err) }
func (e *outputError) Unwrap() error { return e.err }
