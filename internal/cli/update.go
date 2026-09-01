// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package cli

import (
	"github.com/huija/skillmod/internal/i18n"
	"github.com/spf13/cobra"
)

func newUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   i18n.Text("update [names…]"),
		Short: i18n.Text("resolve the latest versions, update the lock, and install"),
		Long:  i18n.Text("With no names, update every entry. Commit-pinned entries, including pseudo-versions, advance to a new pseudo-version at default-branch HEAD."),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := newEngine()
			if err != nil {
				return err
			}
			rep, err := eng.Update(cmd.Context(), args, newIO(cmd))
			if err != nil {
				return err
			}
			return output(cmd, rep)
		},
	}
}
