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
	"strconv"
	"strings"

	"github.com/huija/skillmod/internal/i18n"
)

// Confirmer abstracts user confirmation.
type Confirmer interface {
	// Confirm asks a yes-or-no question and defaults to no.
	Confirm(prompt string) bool
	// Choose returns an option index; for example, options may be ["overwrite", "keep and skip", "abort"].
	Choose(prompt string, options []string) int
}

// MultiSelector can select zero or more options by index.
type MultiSelector interface {
	Confirmer
	ChooseMany(prompt string, options []string) []int
}

// Interactive returns a multi-selector backed by standard input.
// A shared bufio.Reader prevents buffered data from being lost between prompts.
func Interactive(in io.Reader, out io.Writer) MultiSelector {
	r := bufio.NewReader(in)
	return &interactive{r: r, w: out}
}

type interactive struct {
	r *bufio.Reader
	w io.Writer
}

func (i *interactive) Confirm(prompt string) bool {
	fmt.Fprintf(i.w, "%s [y/N] ", prompt)
	line, err := i.r.ReadString('\n')
	if err != nil {
		return false // EOF or a read failure means no; do not spin.
	}
	s := strings.ToLower(strings.TrimSpace(line))
	return s == "y" || s == "yes"
}

func (i *interactive) Choose(prompt string, options []string) int {
	for {
		fmt.Fprintf(i.w, "%s\n", prompt)
		for idx, o := range options {
			fmt.Fprintf(i.w, "  %d) %s\n", idx+1, o)
		}
		fmt.Fprintf(i.w, i18n.Text("choose [1-%d]: "), len(options))
		line, err := i.r.ReadString('\n')
		if err != nil {
			// On EOF or a read failure, choose the final option, which callers reserve as the safest; do not spin.
			fmt.Fprintln(i.w)
			return len(options) - 1
		}
		s := strings.TrimSpace(line)
		for idx := range options {
			if s == fmt.Sprint(idx+1) {
				return idx
			}
		}
		fmt.Fprintln(i.w, i18n.Text("invalid choice; try again"))
	}
}

// ChooseMany returns the selected zero-based indices. An empty line or EOF
// aborts the selection; duplicate numbers are ignored.
func (i *interactive) ChooseMany(prompt string, options []string) []int {
	for {
		fmt.Fprintf(i.w, "%s\n", prompt)
		for idx, option := range options {
			fmt.Fprintf(i.w, "  %d) %s\n", idx+1, option)
		}
		fmt.Fprintf(i.w, i18n.Text("select one or more [1-%d, comma-separated; empty to abort]: "), len(options))
		line, err := i.r.ReadString('\n')
		if err != nil || strings.TrimSpace(line) == "" {
			fmt.Fprintln(i.w)
			return nil
		}
		selected, ok := parseChoices(line, len(options))
		if ok {
			return selected
		}
		fmt.Fprintln(i.w, i18n.Text("invalid selection; try again"))
	}
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
