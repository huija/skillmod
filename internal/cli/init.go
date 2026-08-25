// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package cli

import "github.com/spf13/cobra"

func newInitCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "扫描仓库现有 skill，生成 SKILL.mod 草案",
		Long:  "扫描各平台 skill 目录，经 git ls-remote 匹配来源，逐项确认后生成 SKILL.mod；已有 SKILL.mod 时拒绝执行，--force 可重建（备份为 SKILL.mod.bak）。",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := newEngine()
			if err != nil {
				return err
			}
			rep, err := eng.Init(cmd.Context(), force, newIO(cmd))
			if err != nil {
				return err
			}
			return output(cmd, rep)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "已存在 SKILL.mod 时重新生成，原文件备份为 SKILL.mod.bak")
	return cmd
}
