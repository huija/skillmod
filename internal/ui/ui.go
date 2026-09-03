// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

// Package ui provides interactive confirmation primitives. Non-interactive environments such as CI and agent sessions pass a nil Confirmer,
// causing the engine to use safe defaults by keeping and skipping conflicts or aborting.
package ui

import (
	"bufio"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/huija/skillmod/internal/i18n"
)

// Confirmer abstracts user confirmation.
type Confirmer interface {
	// Confirm asks a yes-or-no question and defaults to no.
	Confirm(prompt string) bool
	// Choose returns an option index; for example, options may be ["overwrite", "keep and skip", "abort"].
	Choose(prompt string, options []string) int
}

// Option is one structured multi-select choice.
type Option struct {
	Label       string
	Description string
	Detail      string
}

// MultiSelector can select zero or more options by index.
type MultiSelector interface {
	Confirmer
	ChooseMany(prompt string, options []Option) []int
}

// Interactive returns a multi-selector backed by terminal input. The caller
// must verify that in is a TTY. Passing the original reader through unchanged
// lets Bubble Tea recognize the terminal and enable raw mode.
func Interactive(in io.Reader, out io.Writer) MultiSelector {
	return &interactive{
		r:        bufio.NewReader(in),
		tuiInput: in,
		w:        out,
	}
}

type interactive struct {
	r        *bufio.Reader
	tuiInput io.Reader
	w        io.Writer
}

type collapsibleMultiSelect struct {
	*huh.MultiSelect[int]
	options       []Option
	toggleDetails key.Binding
	expanded      bool
}

var _ huh.Field = (*collapsibleMultiSelect)(nil)

func (i *interactive) Confirm(prompt string) bool {
	if _, err := fmt.Fprintf(i.w, "%s [y/N] ", prompt); err != nil {
		return false
	}
	line, err := i.r.ReadString('\n')
	if err != nil {
		return false // EOF or a read failure means no; do not spin.
	}
	s := strings.ToLower(strings.TrimSpace(line))
	return s == "y" || s == "yes"
}

func (i *interactive) Choose(prompt string, options []string) int {
	for {
		if _, err := fmt.Fprintf(i.w, "%s\n", prompt); err != nil {
			return len(options) - 1
		}
		for idx, option := range options {
			if _, err := fmt.Fprintf(i.w, "  %d) %s\n", idx+1, option); err != nil {
				return len(options) - 1
			}
		}
		if _, err := fmt.Fprintf(i.w, i18n.Text("choose [1-%d]: "), len(options)); err != nil {
			return len(options) - 1
		}
		line, err := i.r.ReadString('\n')
		if err != nil {
			// On EOF or a read failure, choose the final option, which callers reserve as the safest; do not spin.
			if _, writeErr := fmt.Fprintln(i.w); writeErr != nil {
				return len(options) - 1
			}
			return len(options) - 1
		}
		s := strings.TrimSpace(line)
		for idx := range options {
			if s == fmt.Sprint(idx+1) {
				return idx
			}
		}
		if _, err := fmt.Fprintln(i.w, i18n.Text("invalid choice; try again")); err != nil {
			return len(options) - 1
		}
	}
}

// ChooseMany returns the selected zero-based indices. Terminal input uses
// huh's Bubble Tea multi-select; a selector without TUI input retains the
// comma-separated number interface.
func (i *interactive) ChooseMany(prompt string, options []Option) []int {
	if len(options) == 0 {
		return nil
	}
	if i.tuiInput == nil {
		return i.chooseManyByNumber(prompt, options)
	}

	huhOptions := make([]huh.Option[int], len(options))
	for idx, option := range options {
		// Candidate descriptions stay collapsed so each choice occupies one line.
		huhOptions[idx] = huh.NewOption(cleanLine(option.Label), idx)
	}
	var selected []int
	field := huh.NewMultiSelect[int]().
		Title(cleanLine(prompt)).
		Options(huhOptions...).
		Filterable(false).
		Value(&selected)
	collapsibleField := newCollapsibleMultiSelect(field, options)
	keymap := huh.NewDefaultKeyMap()
	keymap.MultiSelect.Up.SetKeys("up", "left", "k", "ctrl+p")
	keymap.MultiSelect.Up.SetHelp("↑/←", "up")
	keymap.MultiSelect.Down.SetKeys("down", "right", "j", "ctrl+n")
	keymap.MultiSelect.Down.SetHelp("↓/→", "down")
	err := huh.NewForm(huh.NewGroup(collapsibleField)).
		WithInput(i.tuiInput).
		WithOutput(i.w).
		WithAccessible(false).
		WithKeyMap(keymap).
		WithTheme(huh.ThemeCharm()).
		Run()
	if err != nil {
		return nil
	}
	slices.Sort(selected)
	return selected
}

func newCollapsibleMultiSelect(field *huh.MultiSelect[int], options []Option) *collapsibleMultiSelect {
	m := &collapsibleMultiSelect{
		MultiSelect: field,
		options:     options,
		toggleDetails: key.NewBinding(
			key.WithKeys("d", "D"),
			key.WithHelp("d", i18n.Text("show details")),
		),
	}
	m.refreshDetails()
	return m
}

func (m *collapsibleMultiSelect) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok && key.Matches(keyMsg, m.toggleDetails) {
		m.expanded = !m.expanded
		m.refreshDetails()
		return m, nil
	}
	_, cmd := m.MultiSelect.Update(msg)
	m.refreshDetails()
	return m, cmd
}

func (m *collapsibleMultiSelect) KeyBinds() []key.Binding {
	bindings := m.MultiSelect.KeyBinds()
	if len(bindings) == 0 {
		return []key.Binding{m.toggleDetails}
	}
	visible := make([]key.Binding, 0, len(bindings)+1)
	visible = append(visible, bindings[0], m.toggleDetails)
	return append(visible, bindings[1:]...)
}

func (m *collapsibleMultiSelect) refreshDetails() {
	if !m.expanded {
		m.Description("")
		m.toggleDetails.SetHelp("d", i18n.Text("show details"))
		return
	}
	m.toggleDetails.SetHelp("d", i18n.Text("hide details"))
	index, ok := m.Hovered()
	if !ok || index < 0 || index >= len(m.options) {
		m.Description("")
		return
	}
	option := m.options[index]
	details := fmt.Sprintf("%s: %s", i18n.Text("Description"), description(option))
	if command := cleanLine(option.Detail); command != "" {
		details += fmt.Sprintf("\n%s: %s", i18n.Text("Install command"), command)
	}
	m.Description(details)
}

func (i *interactive) chooseManyByNumber(prompt string, options []Option) []int {
	for {
		if _, err := fmt.Fprintf(i.w, "%s\n", cleanLine(prompt)); err != nil {
			return nil
		}
		for idx, option := range options {
			if _, err := fmt.Fprintf(i.w, "  %d) %s\n", idx+1, cleanLine(option.Label)); err != nil {
				return nil
			}
			if _, err := fmt.Fprintf(i.w, "     %s: %s\n", i18n.Text("Description"), description(option)); err != nil {
				return nil
			}
			if detail := cleanLine(option.Detail); detail != "" {
				if _, err := fmt.Fprintf(i.w, "     %s: %s\n", i18n.Text("Install command"), detail); err != nil {
					return nil
				}
			}
		}
		if _, err := fmt.Fprintf(i.w, i18n.Text("select one or more [1-%d, comma-separated; empty to abort]: "), len(options)); err != nil {
			return nil
		}
		line, err := i.r.ReadString('\n')
		if err != nil || strings.TrimSpace(line) == "" {
			if _, writeErr := fmt.Fprintln(i.w); writeErr != nil {
				return nil
			}
			return nil
		}
		selected, ok := parseChoices(line, len(options))
		if ok {
			return selected
		}
		if _, err := fmt.Fprintln(i.w, i18n.Text("invalid selection; try again")); err != nil {
			return nil
		}
	}
}

func description(option Option) string {
	if value := cleanLine(option.Description); value != "" {
		return value
	}
	return i18n.Text("no description")
}

func cleanLine(value string) string {
	value = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			return ' '
		case unicode.IsControl(r):
			return -1
		default:
			return r
		}
	}, value)
	return strings.Join(strings.Fields(value), " ")
}

func parseChoices(line string, optionCount int) ([]int, bool) {
	seen := make(map[int]bool, optionCount)
	var selected []int
	for _, value := range strings.Split(strings.TrimSpace(line), ",") {
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || n < 1 || n > optionCount {
			return nil, false
		}
		idx := n - 1
		if !seen[idx] {
			seen[idx] = true
			selected = append(selected, idx)
		}
	}
	return selected, len(selected) > 0
}
