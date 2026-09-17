// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package cli

import (
	"errors"
	"fmt"

	"github.com/huija/skillmod/internal/engine"
	"github.com/huija/skillmod/internal/i18n"
	"github.com/spf13/cobra"
)

func newRemoveCmd(options *rootOptions) *cobra.Command {
	var all bool
	var agents []string
	var skills []string
	cmd := &cobra.Command{
		Use:   i18n.Text("cli.remove.use"),
		Short: i18n.Text("cli.remove.short"),
		Long:  i18n.Text("cli.remove.long"),
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			targets := splitList(agents)
			if cmd.Flags().Changed("agent") && len(targets) == 0 {
				return fmt.Errorf("%s", i18n.Text("cli.remove.empty_agent"))
			}
			eng, err := options.newEngine()
			if err != nil {
				return err
			}
			// --skill names skills by the same rule as share's: repeated flags
			// and comma-separated values mean the same thing, while positional
			// arguments are shell words and are never split on a comma.
			names := append(splitList(skills), args...)
			rep, err := eng.Remove(cmd.Context(), names, options.newIO(cmd), engine.RemoveOptions{
				All: all, DryRun: options.dryRun, Agents: targets,
			})
			return errors.Join(err, options.output(cmd, rep))
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, i18n.Text("cli.remove.flag_all"))
	cmd.Flags().StringArrayVarP(&agents, "agent", "a", nil, i18n.Text("cli.remove.flag_agent"))
	cmd.Flags().StringArrayVarP(&skills, "skill", "s", nil, i18n.Text("cli.remove.flag_skill"))
	return cmd
}
