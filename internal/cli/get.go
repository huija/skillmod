// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package cli

import (
	"errors"

	"github.com/huija/skillmod/internal/i18n"
	"github.com/spf13/cobra"
)

func newGetCmd(options *rootOptions) *cobra.Command {
	var alias string
	cmd := &cobra.Command{
		Use:   i18n.Text("cli.get.use"),
		Short: i18n.Text("cli.get.short"),
		Long:  i18n.Text("cli.get.long"),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := options.newEngine()
			if err != nil {
				return err
			}
			rep, err := eng.Get(cmd.Context(), args[0], alias, options.newIO(cmd), options.mutationOptions())
			return errors.Join(err, options.output(cmd, rep))
		},
	}
	cmd.Flags().StringVar(&alias, "alias", "", i18n.Text("cli.get.flag_alias"))
	return cmd
}
