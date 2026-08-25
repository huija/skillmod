// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package cli

import "github.com/spf13/cobra"

func newGetCmd() *cobra.Command {
	var alias string
	cmd := &cobra.Command{
		Use:   "get <仓库>[//<子目录>][@<版本>]",
		Short: "按不可变引用拉取 skill，写入 SKILL.mod / SKILL.lock 并安装",
		Long: `版本只接受 semver tag 或 40 位 commit SHA；分支名拒绝（可变引用不可锁定）。
省略版本时按优先级解析：<子目录>/v<最新> → v<最新> → 默认分支 HEAD（记伪版本）。`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := newEngine()
			if err != nil {
				return err
			}
			rep, err := eng.Get(cmd.Context(), args[0], alias, newIO(cmd))
			if err != nil {
				return err
			}
			return output(cmd, rep)
		},
	}
	cmd.Flags().StringVar(&alias, "alias", "", "安装目录别名（解决同名冲突）")
	return cmd
}
