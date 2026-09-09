// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package cli

import (
	"errors"

	"github.com/huija/skillmod/internal/i18n"
	"github.com/spf13/cobra"
)

func newInitCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: i18n.Text("scan existing skills and draft SKILL.mod"),
		Long:  i18n.Text("Scan existing skill directories and directory links, verify source provenance, and generate SKILL.mod and SKILL.lock without changing installed files. Unknown sources remain local. Use --global for user-wide skills; --force backs up an existing SKILL.mod before rebuilding."),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := newEngine()
			if err != nil {
				return err
			}
			rep, err := eng.Init(cmd.Context(), force, newIO(cmd))
			return errors.Join(err, output(cmd, rep))
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, i18n.Text("regenerate an existing SKILL.mod after backing it up as SKILL.mod.bak"))
	return cmd
}
