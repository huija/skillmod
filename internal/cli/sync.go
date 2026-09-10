// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package cli

import (
	"errors"

	"github.com/huija/skillmod/internal/i18n"
	"github.com/spf13/cobra"
)

func newSyncCmd() *cobra.Command {
	var check, relink bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: i18n.Text("cli.sync.short"),
		Long:  i18n.Text("cli.sync.long"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := newEngine()
			if err != nil {
				return err
			}
			io := newIO(cmd)
			io.Relink = relink
			rep, err := eng.Sync(cmd.Context(), check, io)
			return errors.Join(err, output(cmd, rep))
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, i18n.Text("cli.sync.flag_check"))
	cmd.Flags().BoolVar(&relink, "relink", false, i18n.Text("cli.sync.flag_relink"))
	cmd.MarkFlagsMutuallyExclusive("check", "relink")
	return cmd
}
