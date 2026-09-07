// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package main

import (
	"runtime/debug"
	"testing"
)

func TestEffectiveVersion(t *testing.T) {
	tests := []struct {
		name     string
		injected string
		module   string
		readOK   bool
		want     string
	}{
		{name: "injected release", injected: "v1.2.3", module: "v9.9.9", readOK: true, want: "v1.2.3"},
		{name: "module install", injected: "dev", module: "v1.2.3", readOK: true, want: "v1.2.3"},
		{name: "local build", injected: "dev", module: "(devel)", readOK: true, want: "dev"},
		{name: "missing build info", injected: "dev", readOK: false, want: "dev"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			read := func() (*debug.BuildInfo, bool) {
				return &debug.BuildInfo{Main: debug.Module{Version: tt.module}}, tt.readOK
			}
			if got := effectiveVersion(tt.injected, read); got != tt.want {
				t.Errorf("effectiveVersion(%q, module=%q, ok=%t) = %q, want %q", tt.injected, tt.module, tt.readOK, got, tt.want)
			}
		})
	}
}
