// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package main

import (
	"os"

	"github.com/huija/skillmod/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
