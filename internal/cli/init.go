// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package cli

import (
	"github.com/huija/skillmod/internal/i18n"
	"github.com/spf13/cobra"
)

func newInitCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: i18n.Text("scan existing skills and draft SKILL.mod"),
		Long:  i18n.Text("Scan each platform's skill directory, match sources using git ls-remote, confirm each entry, and generate SKILL.mod. An existing SKILL.mod is rejected unless --force rebuilds it after backing it up as SKILL.mod.bak."),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := newEngine()
			if err != nil {
				return err
			}
			rep, err := eng.Init(cmd.Context(), force, newIO(cmd))
			if err != nil {
				return err
			}
			return output(cmd, rep)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, i18n.Text("regenerate an existing SKILL.mod after backing it up as SKILL.mod.bak"))
	return cmd
}
