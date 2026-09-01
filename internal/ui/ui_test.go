// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package ui

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/huija/skillmod/internal/testutil"
)

func TestMain(m *testing.M) { testutil.RunMain(m) }

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
		t.Errorf("Choose = %d, want 1 after an invalid input followed by a valid one", got)
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

func TestChoose_EnglishPrompt(t *testing.T) {
	var out bytes.Buffer
	c := Interactive(strings.NewReader("x\n2\n"), &out)
	if got := c.Choose("pick", []string{"first", "second"}); got != 1 {
		t.Fatalf("Choose = %d, want 1", got)
	}
	if text := out.String(); !strings.Contains(text, "choose [1-2]") || !strings.Contains(text, "invalid choice; try again") {
		t.Fatalf("English prompt = %q", text)
	}
}

func TestChooseMany_ValidAndRetry(t *testing.T) {
	var out bytes.Buffer
	c := Interactive(strings.NewReader("1,x\n2, 1, 2\n"), &out)
	got := c.ChooseMany("pick", []string{"first", "second", "third"})
	if want := []int{1, 0}; !slices.Equal(got, want) {
		t.Fatalf("ChooseMany = %v, want %v", got, want)
	}
	if text := out.String(); !strings.Contains(text, "invalid selection; try again") || !strings.Contains(text, "comma-separated") {
		t.Fatalf("ChooseMany prompt = %q", text)
	}
}

func TestChooseMany_EmptyAndEOFAbort(t *testing.T) {
	for _, input := range []string{"\n", ""} {
		c := Interactive(strings.NewReader(input), &bytes.Buffer{})
		if got := c.ChooseMany("pick", []string{"first", "second"}); got != nil {
			t.Errorf("ChooseMany(%q) = %v, want nil", input, got)
		}
	}
}
