// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"runtime/debug"

	"github.com/huija/skillmod/internal/cli"
)

// version is set by Make or GoReleaser. Direct module installs fall back to
// the version recorded by the Go toolchain.
var version = "dev"

func main() {
	cli.Version = effectiveVersion(version, debug.ReadBuildInfo)
	os.Exit(cli.Execute())
}

func effectiveVersion(injected string, readBuildInfo func() (*debug.BuildInfo, bool)) string {
	if injected != "" && injected != "dev" {
		return injected
	}
	info, ok := readBuildInfo()
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return "dev"
	}
	return info.Main.Version
}
