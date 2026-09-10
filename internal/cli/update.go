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
		Use:   i18n.Text("cli.update.use"),
		Short: i18n.Text("cli.update.short"),
		Long:  i18n.Text("cli.update.long"),
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
	cmd.Flags().BoolVar(&allowDowngrade, "allow-downgrade", false, i18n.Text("cli.update.flag_allow_downgrade"))
	return cmd
}
