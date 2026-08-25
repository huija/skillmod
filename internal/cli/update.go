// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package cli

import "github.com/spf13/cobra"

func newUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update [名称…]",
		Short: "重新解析最新版本，更新 lock 并安装",
		Long:  "不带名称时作用于全部条目；commit 钉住的条目（含伪版本）升到默认分支 HEAD 的新伪版本。",
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := newEngine()
			if err != nil {
				return err
			}
			rep, err := eng.Update(cmd.Context(), args, newIO(cmd))
			if err != nil {
				return err
			}
			return output(cmd, rep)
		},
	}
}
