// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package cli

import (
	"github.com/huija/skillmod/internal/i18n"
	"github.com/spf13/cobra"
)

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: i18n.Text("list all declared entries and their status (installed / missing / upgradable)"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := newEngine()
			if err != nil {
				return err
			}
			rep, err := eng.List(cmd.Context(), newIO(cmd))
			if err != nil {
				return err
			}
			return output(cmd, rep)
		},
	}
}
