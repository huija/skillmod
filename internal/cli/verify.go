// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package cli

import "github.com/spf13/cobra"

func newVerifyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "verify",
		Short: "全量校验安装状态与 SKILL.lock 是否一致（只读，CI 消费退出码）",
		Long:  "检出漂移时退出码为 2；与 sync --check 为同一实现。",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := newEngine()
			if err != nil {
				return err
			}
			rep, err := eng.Verify(cmd.Context(), newIO(cmd))
			_ = output(cmd, rep)
			return err
		},
	}
}
