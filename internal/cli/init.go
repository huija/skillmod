// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package cli

import (
	"errors"

	"github.com/huija/skillmod/internal/i18n"
	"github.com/spf13/cobra"
)

func newInitCmd(options *rootOptions) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: i18n.Text("cli.init.short"),
		Long:  i18n.Text("cli.init.long"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := options.newEngine()
			if err != nil {
				return err
			}
			rep, err := eng.Init(cmd.Context(), force, options.newIO(cmd), options.mutationOptions())
			return errors.Join(err, options.output(cmd, rep))
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, i18n.Text("cli.init.flag_force"))
	return cmd
}
