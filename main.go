// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package main

import (
	"os"

	"github.com/huija/skillmod/internal/cli"
)

// version is set to the release tag by GoReleaser. Local builds retain dev.
var version = "dev"

func main() {
	cli.Version = version
	os.Exit(cli.Execute())
}
