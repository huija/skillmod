// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package cli

import (
	"github.com/huija/skillmod/internal/i18n"
	"github.com/spf13/cobra"
)

func newPruneCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "prune",
		Short: i18n.Text("remove installed files left by stale entries (list and confirm before deleting)"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := newEngine()
			if err != nil {
				return err
			}
			rep, err := eng.Prune(cmd.Context(), newIO(cmd))
			if err != nil {
				return err
			}
			return output(cmd, rep)
		},
	}
}
