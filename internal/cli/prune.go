// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package cli

import "github.com/spf13/cobra"

func newPruneCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "prune",
		Short: "清理过期条目的残留安装文件（前列清单，确认后才删除）",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := newEngine()
			if err != nil {
				return err
			}
			rep, err := eng.Prune(cmd.Context(), newIO(cmd))
			if err != nil {
				return err
			}
			return output(cmd, rep)
		},
	}
}
