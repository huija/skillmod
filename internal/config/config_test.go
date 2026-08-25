// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package config

import "testing"

func TestDefaultAgentTarget(t *testing.T) {
	cfg := Default()
	if len(cfg.Agents) != 1 || cfg.Agents[0] != "agents" {
		t.Fatalf("Default Agents = %v, want [agents]", cfg.Agents)
	}
}
