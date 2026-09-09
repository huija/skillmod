// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package cli

import (
	"errors"

	"github.com/huija/skillmod/internal/i18n"
	"github.com/spf13/cobra"
)

func newUpdateCmd() *cobra.Command {
	var allowDowngrade bool
	cmd := &cobra.Command{
		Use:   i18n.Text("update [names…]"),
		Short: i18n.Text("resolve the latest versions, update the lock, and install"),
		Long:  i18n.Text("With no names, update every entry. Commit-pinned entries, including pseudo-versions, advance to a new pseudo-version at default-branch HEAD."),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := newEngine()
			if err != nil {
				return err
			}
			io := newIO(cmd)
			io.AllowDowngrade = allowDowngrade
			rep, err := eng.Update(cmd.Context(), args, io)
			return errors.Join(err, output(cmd, rep))
		},
	}
	cmd.Flags().BoolVar(&allowDowngrade, "allow-downgrade", false, i18n.Text("allow update to select a lower semantic version when newer tags disappeared"))
	return cmd
}
