// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package cli

import "github.com/spf13/cobra"

func newSyncCmd() *cobra.Command {
	var check bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "按 SKILL.lock 将本地 skill 目录对齐到锁定状态",
		Long:  "幂等、可校验、失败回滚；永不自动删除已安装文件（清理走 prune）。--check 只校验不写文件，是 verify 的别名。",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := newEngine()
			if err != nil {
				return err
			}
			rep, err := eng.Sync(cmd.Context(), check, newIO(cmd))
			_ = output(cmd, rep)
			return err
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "只校验不修改（skillmod verify 的别名）")
	return cmd
}
