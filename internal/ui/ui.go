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
	Confirm(prompt string) (bool, error)
	// Choose returns an option index; for example, options may be ["overwrite", "keep and skip", "abort"].
	Choose(prompt string, options []string) (int, error)
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
	ChooseMany(prompt string, options []Option) ([]int, error)
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

func (i *interactive) Confirm(prompt string) (bool, error) {
	if _, err := fmt.Fprintf(i.w, "%s [y/N] ", prompt); err != nil {
		return false, err
	}
	line, err := i.r.ReadString('\n')
	if err != nil {
		return false, err
	}
	s := strings.ToLower(strings.TrimSpace(line))
	return s == "y" || s == "yes", nil
}

func (i *interactive) Choose(prompt string, options []string) (int, error) {
	for {
		if _, err := fmt.Fprintf(i.w, "%s\n", prompt); err != nil {
			return 0, err
		}
		for idx, option := range options {
			if _, err := fmt.Fprintf(i.w, "  %d) %s\n", idx+1, option); err != nil {
				return 0, err
			}
		}
		if _, err := fmt.Fprintf(i.w, i18n.Text("ui.choose"), len(options)); err != nil {
			return 0, err
		}
		line, err := i.r.ReadString('\n')
		if err != nil {
			return 0, err
		}
		s := strings.TrimSpace(line)
		for idx := range options {
			if s == fmt.Sprint(idx+1) {
				return idx, nil
			}
		}
		if _, err := fmt.Fprintln(i.w, i18n.Text("ui.invalid_choice_try_again")); err != nil {
			return 0, err
		}
	}
}

// ChooseMany returns the selected zero-based indices using huh's Bubble Tea
// multi-select over the terminal input the selector was constructed with.
func (i *interactive) ChooseMany(prompt string, options []Option) ([]int, error) {
	if len(options) == 0 {
		return nil, nil
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
		return nil, err
	}
	// Huh clears the form when it quits. Preserve explicitly expanded details
	// as ordinary terminal output so confirming the selection does not make the
	// second line disappear before the user can read it.
	if collapsibleField.expanded {
		if details := collapsibleField.details(); details != "" {
			if _, err := fmt.Fprintln(i.w, details); err != nil {
				return nil, err
			}
		}
	}
	slices.Sort(selected)
	return selected, nil
}

func newCollapsibleMultiSelect(field *huh.MultiSelect[int], options []Option) *collapsibleMultiSelect {
	m := &collapsibleMultiSelect{
		MultiSelect: field,
		options:     options,
		toggleDetails: key.NewBinding(
			key.WithKeys("d", "D"),
			key.WithHelp("d", i18n.Text("ui.show_details")),
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
		m.toggleDetails.SetHelp("d", i18n.Text("ui.show_details"))
		return
	}
	m.toggleDetails.SetHelp("d", i18n.Text("ui.hide_details"))
	index, ok := m.Hovered()
	if !ok || index < 0 || index >= len(m.options) {
		m.Description("")
		return
	}
	m.Description(m.details())
}

func (m *collapsibleMultiSelect) details() string {
	index, ok := m.Hovered()
	if !ok || index < 0 || index >= len(m.options) {
		return ""
	}
	option := m.options[index]
	details := fmt.Sprintf("%s: %s", i18n.Text("ui.description"), description(option))
	if command := cleanLine(option.Detail); command != "" {
		details += fmt.Sprintf("\n%s: %s", i18n.Text("ui.install_command"), command)
	}
	return details
}

func description(option Option) string {
	if value := cleanLine(option.Description); value != "" {
		return value
	}
	return i18n.Text("engine.get.description")
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
