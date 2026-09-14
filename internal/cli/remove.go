// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package cli

import (
	"errors"

	"github.com/huija/skillmod/internal/i18n"
	"github.com/spf13/cobra"
)

func newRemoveCmd(options *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   i18n.Text("cli.remove.use"),
		Short: i18n.Text("cli.remove.short"),
		Long:  i18n.Text("cli.remove.long"),
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := options.newEngine()
			if err != nil {
				return err
			}
			rep, err := eng.Remove(cmd.Context(), args, options.newIO(cmd), options.mutationOptions())
			return errors.Join(err, options.output(cmd, rep))
		},
	}
}
