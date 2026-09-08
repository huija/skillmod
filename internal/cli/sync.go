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
	var check bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: i18n.Text("align local skill directories with the state pinned in SKILL.lock"),
		Long:  i18n.Text("Idempotent and verifiable, with rollback on failure. Installed files are never deleted automatically; use prune to clean them. --check only verifies and is an alias for verify."),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := newEngine()
			if err != nil {
				return err
			}
			rep, err := eng.Sync(cmd.Context(), check, newIO(cmd))
			return errors.Join(err, output(cmd, rep))
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, i18n.Text("verify without modifying anything (alias for skillmod verify)"))
	return cmd
}
