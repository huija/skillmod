// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

// Package cli provides the entry layer for the seven subcommands: validate arguments, call the engine, and format output.
// All business logic lives in internal/engine; this layer only handles I/O.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/huija/skillmod/internal/config"
	"github.com/huija/skillmod/internal/engine"
	"github.com/huija/skillmod/internal/i18n"
	"github.com/huija/skillmod/internal/source"
	"github.com/huija/skillmod/internal/store"
	"github.com/huija/skillmod/internal/ui"
)

// Exit-code contract.
const (
	ExitOK    = 0
	ExitError = 1
	ExitDrift = 2 // verify detected drift; intended for CI use (AC-12)
)

// Global flags.
var (
	flagJSON   bool
	flagYes    bool
	flagDryRun bool

	// Version is set by main from the build-time version metadata.
	Version = "dev"
)

// Error renders the what/why/advice template required by the PRD.
type Error struct {
	What   string // what happened
	Why    string // diagnosed cause
	Advice string // suggested next command or action
}

func (e *Error) Error() string {
	s := i18n.Text("Error: ") + e.What
	if e.Why != "" {
		s += i18n.Text("\nCause: ") + e.Why
	}
	if e.Advice != "" {
		s += i18n.Text("\nAdvice: ") + e.Advice
	}
	return s
}

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
	pf.BoolVar(&flagJSON, "json", false, i18n.Text("output structured results as JSON"))
	pf.BoolVar(&flagYes, "yes", false, i18n.Text("skip interactive confirmation (for CI)"))
	pf.BoolVar(&flagDryRun, "dry-run", false, i18n.Text("print the execution plan without writing files"))

	root.AddCommand(
		newInitCmd(),
		newGetCmd(),
		newSyncCmd(),
		newListCmd(),
		newUpdateCmd(),
		newPruneCmd(),
		newVerifyCmd(),
	)
	return root
}

// Execute runs the root command and maps errors to exit codes.
func Execute() int {
	if err := NewRootCmd().Execute(); err != nil {
		var drift *engine.DriftError
		if errors.As(err, &drift) {
			return ExitDrift
		}
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}
	return ExitOK
}

// newEngine creates an engine using the current working directory as the project root.
func newEngine() (*engine.Engine, error) {
	root, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	s, err := store.Open()
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	return &engine.Engine{
		Root:   root,
		Source: &source.Source{VCSRoot: s.VCSRoot()},
		Store:  s,
		Config: cfg,
	}, nil
}

// newIO configures I/O channels from global flags and terminal state.
func newIO(cmd *cobra.Command) engine.IO {
	io := engine.IO{
		Out:    cmd.OutOrStdout(),
		Err:    cmd.ErrOrStderr(),
		Yes:    flagYes,
		DryRun: flagDryRun,
	}
	if isTerminal(os.Stdin) {
		io.Confirm = ui.Interactive(os.Stdin, cmd.ErrOrStderr())
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
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), string(data))
	return nil
}
