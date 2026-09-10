// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package cli

import (
	"errors"

	"github.com/huija/skillmod/internal/i18n"
	"github.com/spf13/cobra"
)

func newWhyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   i18n.Text("cli.why.use"),
		Short: i18n.Text("cli.why.short"),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := newEngine()
			if err != nil {
				return err
			}
			rep, err := eng.Why(cmd.Context(), args[0], newIO(cmd))
			return errors.Join(err, output(cmd, rep))
		},
	}
}
