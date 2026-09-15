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

func newSyncCmd(options *rootOptions) *cobra.Command {
	var check, relink bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: i18n.Text("cli.sync.short"),
		Long:  i18n.Text("cli.sync.long"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := options.newEngine()
			if err != nil {
				return err
			}
			rep, err := eng.Sync(cmd.Context(), engine.SyncOptions{
				CheckOnly: check, Relink: relink, DryRun: options.dryRun,
			}, options.newIO(cmd))
			return errors.Join(err, options.output(cmd, rep))
		},
	}
	cmd.Flags().BoolVarP(&check, "check", "c", false, i18n.Text("cli.sync.flag_check"))
	cmd.Flags().BoolVarP(&relink, "relink", "r", false, i18n.Text("cli.sync.flag_relink"))
	cmd.MarkFlagsMutuallyExclusive("check", "relink")
	return cmd
}
