// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package cli

import (
	"errors"

	"github.com/huija/skillmod/internal/engine"
	"github.com/huija/skillmod/internal/i18n"
	"github.com/spf13/cobra"
)

func newUpdateCmd(options *rootOptions) *cobra.Command {
	var allowDowngrade bool
	cmd := &cobra.Command{
		Use:   i18n.Text("cli.update.use"),
		Short: i18n.Text("cli.update.short"),
		Long:  i18n.Text("cli.update.long"),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := options.newEngine()
			if err != nil {
				return err
			}
			rep, err := eng.Update(cmd.Context(), args, engine.UpdateOptions{
				AllowDowngrade: allowDowngrade, DryRun: options.dryRun,
			}, options.newIO(cmd))
			return errors.Join(err, options.output(cmd, rep))
		},
	}
	// --allow-downgrade deliberately has no shorthand: it overrides a safety
	// check, so it is typed rarely and earned by spelling it out.
	cmd.Flags().BoolVar(&allowDowngrade, "allow-downgrade", false, i18n.Text("cli.update.flag_allow_downgrade"))
	return cmd
}
