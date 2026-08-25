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
	"github.com/huija/skillmod/internal/source"
	"github.com/huija/skillmod/internal/store"
	"github.com/huija/skillmod/internal/ui"
)

// Exit-code contract (dev-design §9).
const (
	ExitOK    = 0
	ExitError = 1
	ExitDrift = 2 // verify detected drift; intended for CI use (AC-12)
)

// Global flags (dev-design §9); --json is the integration surface for the v0.2 meta-skill.
var (
	flagJSON   bool
	flagYes    bool
	flagDryRun bool
)

// Error renders the what/why/advice template required by the PRD.
type Error struct {
	What   string // what happened
	Why    string // diagnosed cause
	Advice string // suggested next command or action
}

func (e *Error) Error() string {
	s := "错误: " + e.What
	if e.Why != "" {
		s += "\n原因: " + e.Why
	}
	if e.Advice != "" {
		s += "\n建议: " + e.Advice
	}
	return s
}

// NewRootCmd assembles the root command and all subcommands.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "skillmod",
		Short: "go mod for Agent Skills：SKILL.mod 声明 + SKILL.lock 锁定 + sync 对齐",
		Long: `skillmod 用 go mod 的同构方案管理 agent skill 依赖：
SKILL.mod 声明 + SKILL.lock 内容锁定（dirhash）+ skillmod sync 幂等对齐，
保证任何机器得到完全一致的 skill 集合。`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	pf := root.PersistentFlags()
	pf.BoolVar(&flagJSON, "json", false, "以 JSON 输出结构化结果")
	pf.BoolVar(&flagYes, "yes", false, "跳过交互确认（CI 用）")
	pf.BoolVar(&flagDryRun, "dry-run", false, "只输出执行计划，不写任何文件")

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
