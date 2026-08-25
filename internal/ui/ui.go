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
	"strings"
)

// Confirmer abstracts user confirmation.
type Confirmer interface {
	// Confirm asks a yes-or-no question and defaults to no.
	Confirm(prompt string) bool
	// Choose returns an option index; for example, options may be ["overwrite", "keep and skip", "abort"].
	Choose(prompt string, options []string) int
}

// Interactive returns a confirmer backed by standard input.
// A shared bufio.Reader prevents buffered data from being lost between prompts.
func Interactive(in io.Reader, out io.Writer) Confirmer {
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
		fmt.Fprintf(i.w, "请选择 [1-%d]: ", len(options))
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
		fmt.Fprintln(i.w, "无效选择，请重试")
	}
}
