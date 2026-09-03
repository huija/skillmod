// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package ui

import (
	"bufio"
	"bytes"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
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
	c := &interactive{
		r: bufio.NewReader(strings.NewReader("1,x\n2, 1, 2\n")),
		w: &out,
	}
	got := c.ChooseMany("pick", []Option{
		{Label: "first", Description: "first description", Detail: "install first"},
		{Label: "second", Description: "second description", Detail: "install second"},
		{Label: "third", Description: "third description", Detail: "install third"},
	})
	if want := []int{1, 0}; !slices.Equal(got, want) {
		t.Errorf("ChooseMany(valid retry input) = %v, want %v", got, want)
	}
	if text := out.String(); !strings.Contains(text, "invalid selection; try again") ||
		!strings.Contains(text, "comma-separated") ||
		!strings.Contains(text, "Description: first description") ||
		!strings.Contains(text, "Install command: install first") {
		t.Errorf("ChooseMany(valid retry input) output = %q, want retry and structured option details", text)
	}
}

func TestChooseMany_EmptyAndEOFAbort(t *testing.T) {
	for _, input := range []string{"\n", ""} {
		c := &interactive{
			r: bufio.NewReader(strings.NewReader(input)),
			w: &bytes.Buffer{},
		}
		options := []Option{{Label: "first"}, {Label: "second"}}
		if got := c.ChooseMany("pick", options); got != nil {
			t.Errorf("ChooseMany(%q) = %v, want nil", input, got)
		}
	}
}

func TestInteractivePassesOriginalInputToHuh(t *testing.T) {
	in := strings.NewReader(" \r")
	selector, ok := Interactive(in, &bytes.Buffer{}).(*interactive)
	if !ok {
		t.Fatalf("Interactive() type = %T, want *interactive", selector)
	}
	if selector.tuiInput != in {
		t.Errorf("Interactive() TUI input = %T, want original input %T", selector.tuiInput, in)
	}
}

func TestChooseMany_HuhTogglesWithSpaceAndConfirmsWithEnter(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	// Move to the second option, select it, move to the third, select it, and confirm.
	input := strings.NewReader("\x1b[B \x1b[B \r")
	var out bytes.Buffer
	selector := &interactive{
		tuiInput: input,
		w:        &out,
	}
	options := []Option{
		{Label: "first", Description: "first description", Detail: "install first"},
		{Label: "second", Description: "second description", Detail: "install second"},
		{Label: "third", Description: "third description", Detail: "install third"},
	}

	got := selector.ChooseMany("pick", options)
	if want := []int{1, 2}; !slices.Equal(got, want) {
		t.Errorf("ChooseMany(down, space, down, space, enter) = %v, want %v; output = %q", got, want, out.String())
	}
	if text := out.String(); strings.Contains(text, "first description") ||
		strings.Contains(text, "second description") ||
		strings.Contains(text, "install second") {
		t.Errorf("ChooseMany(options with descriptions) output = %q, want descriptions and details collapsed", text)
	}
}

func TestChooseMany_HuhKeepsEveryOptionOnOneLine(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	input := strings.NewReader(" \r")
	var out bytes.Buffer
	selector := &interactive{
		tuiInput: input,
		w:        &out,
	}
	options := []Option{{Label: "first\nforged line", Description: "hidden"}, {Label: "second"}}

	got := selector.ChooseMany("pick", options)
	if want := []int{0}; !slices.Equal(got, want) {
		t.Errorf("ChooseMany(space, enter) = %v, want %v; output = %q", got, want, out.String())
	}
	if text := out.String(); strings.Contains(text, "\nforged line") || strings.Contains(text, "hidden") {
		t.Errorf("ChooseMany(option with newline) output = %q, want a sanitized one-line option", text)
	}
}

func TestChooseMany_HuhUsesArrowKeysWhenTERMIsDumb(t *testing.T) {
	t.Setenv("TERM", "dumb")
	// Right moves to the second option, space selects it, and left returns to
	// the first option so it can also be selected before enter confirms.
	input := strings.NewReader("\x1b[C \x1b[D \r")
	var out bytes.Buffer
	selector := &interactive{
		tuiInput: input,
		w:        &out,
	}
	options := []Option{{Label: "first"}, {Label: "second"}, {Label: "third"}}

	got := selector.ChooseMany("pick", options)
	if want := []int{0, 1}; !slices.Equal(got, want) {
		t.Errorf("ChooseMany(right, space, left, space, enter; TERM=dumb) = %v, want %v; output = %q", got, want, out.String())
	}
	if text := out.String(); strings.Contains(text, "Input a number") {
		t.Errorf("ChooseMany(TERM=dumb) output = %q, want Bubble Tea mode instead of numeric fallback", text)
	}
}

func TestCollapsibleMultiSelectTogglesDetailsAndFollowsCursor(t *testing.T) {
	options := []Option{
		{Label: "first", Description: "first description", Detail: "skillmod get github.com/acme/first"},
		{Label: "second", Description: "second description", Detail: "skillmod get github.com/acme/second"},
	}
	field := huh.NewMultiSelect[int]().
		Title("pick").
		Options(huh.NewOption("first", 0), huh.NewOption("second", 1)).
		Filterable(false)
	collapsible := newCollapsibleMultiSelect(field, options)
	collapsible.WithKeyMap(huh.NewDefaultKeyMap())
	collapsible.WithWidth(80)
	collapsible.WithHeight(10)
	collapsible.Focus()

	if view := collapsible.View(); strings.Contains(view, "first description") || strings.Contains(view, "github.com/acme/first") {
		t.Errorf("collapsibleMultiSelect.View() collapsed = %q, want details hidden", view)
	}
	collapsible.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if view := collapsible.View(); !strings.Contains(view, "first description") || !strings.Contains(view, "github.com/acme/first") {
		t.Errorf("collapsibleMultiSelect.Update(d) view = %q, want first option details expanded", view)
	}
	collapsible.Update(tea.KeyMsg{Type: tea.KeyDown})
	if view := collapsible.View(); !strings.Contains(view, "second description") || strings.Contains(view, "first description") {
		t.Errorf("collapsibleMultiSelect.Update(down) view = %q, want expanded details to follow cursor", view)
	}
	collapsible.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if view := collapsible.View(); strings.Contains(view, "second description") || strings.Contains(view, "github.com/acme/second") {
		t.Errorf("collapsibleMultiSelect.Update(d, down, d) view = %q, want details collapsed", view)
	}
}
