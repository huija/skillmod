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
	ExitDrift   = 2 // verify detected drift; intended for CI use (AC-12)
	ExitPartial = 3 // safe conflict handling skipped at least one target
)

// Global flags.
var (
	flagInstallMode string
	flagGlobal      bool
	flagJSON        bool
	flagYes         bool
	flagDryRun      bool

	// Version is set by main from the build-time version metadata.
	Version = "dev"
)

// NewRootCmd assembles the root command and all subcommands.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:     "skillmod",
		Version: Version,
		Short:   i18n.Text("go mod for Agent Skills: SKILL.mod declarations + SKILL.lock pinning + sync alignment"),
		Long: i18n.Text(`skillmod manages Agent Skill dependencies using a workflow modeled after go mod:
SKILL.mod declarations + SKILL.lock content pinning (dirhash) + idempotent skillmod sync,
ensuring every machine gets exactly the same set of skills.`),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	pf := root.PersistentFlags()
	pf.StringVar(&flagInstallMode, "install-mode", "", i18n.Text("installation mode: auto or copy (overrides config)"))
	pf.BoolVar(&flagGlobal, "global", false, i18n.Text("manage user-wide skills instead of the current project"))
	pf.BoolVar(&flagJSON, "json", false, i18n.Text("output structured results as JSON"))
	pf.BoolVar(&flagYes, "yes", false, i18n.Text("skip interactive confirmation (for CI)"))
	pf.BoolVar(&flagDryRun, "dry-run", false, i18n.Text("print the execution plan without writing files"))

	root.AddCommand(
		newInitCmd(),
		newGetCmd(),
		newSyncCmd(),
		newListCmd(),
		newWhyCmd(),
		newUpdateCmd(),
		newRemoveCmd(),
		newPruneCmd(),
		newVerifyCmd(),
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
func newEngine() (*engine.Engine, error) {
	var root string
	var err error
	if flagGlobal {
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
	if flagInstallMode != "" {
		cfg.InstallMode = install.Mode(flagInstallMode)
	}
	if err := install.ValidateMode(cfg.InstallMode); err != nil {
		return nil, err
	}
	manifestRoot := ""
	if flagGlobal {
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
func newIO(cmd *cobra.Command) engine.IO {
	out := cmd.OutOrStdout()
	if flagJSON {
		out = cmd.ErrOrStderr()
	}
	io := engine.IO{
		Out:    out,
		Yes:    flagYes,
		DryRun: flagDryRun,
	}
	if !flagYes && isTerminal(os.Stdin) {
		io.Confirm = ui.Interactive(os.Stdin, cmd.ErrOrStderr())
	}
	if isTerminal(os.Stderr) {
		io.Progress = ui.AnimatedProgress(cmd.ErrOrStderr())
	}
	return io
}

func isTerminal(f *os.File) bool {
	// Use ioctl because /dev/null is also ModeCharDevice and a Stat-based check would misclassify it.
	return term.IsTerminal(int(f.Fd()))
}

// output renders structured JSON for --json; otherwise the engine has already printed a summary.
func output(cmd *cobra.Command, rep *engine.Report) error {
	if !flagJSON || rep == nil {
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
