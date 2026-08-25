// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package cli

import "github.com/spf13/cobra"

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "列出全部声明条目及状态（已安装 / 缺失 / 可升级）",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := newEngine()
			if err != nil {
				return err
			}
			rep, err := eng.List(cmd.Context(), newIO(cmd))
			if err != nil {
				return err
			}
			return output(cmd, rep)
		},
	}
}
