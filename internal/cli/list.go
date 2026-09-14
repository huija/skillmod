// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package cli

import (
	"errors"

	"github.com/huija/skillmod/internal/i18n"
	"github.com/spf13/cobra"
)

func newListCmd(options *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: i18n.Text("cli.list.short"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := options.newEngine()
			if err != nil {
				return err
			}
			rep, err := eng.List(cmd.Context(), options.newIO(cmd))
			return errors.Join(err, options.output(cmd, rep))
		},
	}
}
