// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package cli

import (
	"github.com/huija/skillmod/internal/i18n"
	"github.com/spf13/cobra"
)

func newVerifyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "verify",
		Short: i18n.Text("verify every installation against SKILL.lock (read-only, with CI-friendly exit codes)"),
		Long:  i18n.Text("Exit with status 2 when drift is detected. Uses the same implementation as sync --check."),
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
