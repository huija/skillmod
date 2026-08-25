// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package ui

import (
	"bytes"
	"strings"
	"testing"
)

func TestChoose_EOF(t *testing.T) {
	// EOF must return the final, safest option without spinning; this guards a bug found in smoke testing.
	c := Interactive(strings.NewReader(""), &bytes.Buffer{})
	if got := c.Choose("pick", []string{"a", "b", "c"}); got != 2 {
		t.Errorf("Choose on EOF = %d, want 2", got)
	}
}

func TestConfirm_EOF(t *testing.T) {
	c := Interactive(strings.NewReader(""), &bytes.Buffer{})
	if c.Confirm("ok?") {
		t.Error("Confirm on EOF = true, want false")
	}
}

func TestChoose_ValidAndRetry(t *testing.T) {
	c := Interactive(strings.NewReader("x\n2\n"), &bytes.Buffer{})
	if got := c.Choose("pick", []string{"a", "b"}); got != 1 {
		t.Errorf("Choose = %d, want 1（先无效输入再有效）", got)
	}
}

func TestConfirm_Yes(t *testing.T) {
	for _, in := range []string{"y\n", "Y\n", "yes\n", " YES \n"} {
		c := Interactive(strings.NewReader(in), &bytes.Buffer{})
		if !c.Confirm("ok?") {
			t.Errorf("Confirm(%q) = false, want true", in)
		}
	}
}
