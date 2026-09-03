// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package cli

import (
	"github.com/huija/skillmod/internal/i18n"
	"github.com/spf13/cobra"
)

func newGetCmd() *cobra.Command {
	var alias string
	cmd := &cobra.Command{
		Use:   i18n.Text("get <repository>[//<subdirectory-or-skill-name>][@<version>]"),
		Short: i18n.Text("fetch a skill by immutable reference, update SKILL.mod / SKILL.lock, and install it"),
		Long: i18n.Text(`Versions must be semver tags or 40-character commit SHAs; branch names are rejected because they are mutable.
When omitted, the version is resolved in this order: <subdirectory>/v<latest> → v<latest> → default-branch HEAD (recorded as a pseudo-version).
A single segment after // first addresses an exact root subdirectory, then falls back to a unique skill name anywhere below skills/. When a subdirectory is omitted, get discovers SKILL.md at the repository root and recursively below skills/. Interactive terminals show a compact, colored one-line list; displayed commands omit the redundant https:// prefix and prefer a unique skill-name shorthand when safe. Use ↑/← for the previous item, ↓/→ for the next item, space to toggle selections, d to show or hide the current description and command, and enter to confirm. --yes installs every discovered skill.`),
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
	cmd.Flags().StringVar(&alias, "alias", "", i18n.Text("installation directory alias (resolves name conflicts)"))
	return cmd
}
