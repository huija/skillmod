// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package cli

import (
	"errors"
	"strings"

	"github.com/spf13/cobra"

	"github.com/huija/skillmod/internal/engine"
	"github.com/huija/skillmod/internal/i18n"
)

func newShareCmd(options *rootOptions) *cobra.Command {
	var agents []string
	var skills []string
	var all bool
	var onConflict string
	var remove []string
	cmd := &cobra.Command{
		Use:   i18n.Text("cli.share.use"),
		Short: i18n.Text("cli.share.short"),
		Long:  i18n.Text("cli.share.long"),
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := options.newEngine()
			if err != nil {
				return err
			}
			// Positional arguments name skills, so `skillmod share gh-fix-ci`
			// reads like `skillmod get ...` before it. Repeated --skill flags
			// and comma-separated values mean the same thing, matching --agent;
			// positional arguments are shell words and are never split on a
			// comma. --remove edits the agent lists of specific entries, so it
			// takes the same skill selection; the engine rejects mixing it with
			// the options that link things back in.
			skills = append(splitList(skills), args...)
			rep, err := eng.Share(cmd.Context(), engine.ShareOptions{
				Skills:     skills,
				All:        all,
				Agents:     splitList(agents),
				Remove:     splitList(remove),
				OnConflict: onConflict,
			}, options.newIO(cmd), options.mutationOptions())
			return errors.Join(err, options.output(cmd, rep))
		},
	}
	cmd.Flags().StringArrayVarP(&skills, "skill", "s", nil, i18n.Text("cli.share.flag_skill"))
	cmd.Flags().StringArrayVarP(&agents, "agent", "a", nil, i18n.Text("cli.share.flag_agent"))
	cmd.Flags().BoolVar(&all, "all", false, i18n.Text("cli.share.flag_all"))
	cmd.Flags().StringVar(&onConflict, "on-conflict", "", i18n.Text("cli.share.flag_on_conflict"))
	cmd.Flags().StringArrayVarP(&remove, "remove", "r", nil, i18n.Text("cli.share.flag_remove"))
	return cmd
}

// splitList accepts both repeated flags and comma-separated values, so
// `--agent claude,codex` and `--agent claude --agent codex` mean one thing.
func splitList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}
