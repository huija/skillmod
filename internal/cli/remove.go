// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package cli

import (
	"errors"

	"github.com/huija/skillmod/internal/i18n"
	"github.com/spf13/cobra"
)

func newRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   i18n.Text("remove <names…>"),
		Short: i18n.Text("remove declarations and clean managed installations"),
		Long:  i18n.Text("Select by published name or installation alias. Locally modified installations are kept and reported as partial completion."),
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := newEngine()
			if err != nil {
				return err
			}
			rep, err := eng.Remove(cmd.Context(), args, newIO(cmd))
			return errors.Join(err, output(cmd, rep))
		},
	}
}
