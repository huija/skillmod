// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestProgressModelLayersAndReplacesMessages(t *testing.T) {
	model := newProgressModel(cleanMessages([]string{"first phase", "second\nphase", "third phase"}))
	if view := model.View(); !strings.Contains(view, "first phase") ||
		!strings.Contains(view, "second phase · third phase") || strings.Count(view, "\n") != 1 {
		t.Errorf("progressModel.View() = %q, want a primary line and one concatenated detail line", view)
	}

	updated, _ := model.Update(model.spinner.Tick())
	model = updated.(progressModel)
	if view := model.View(); !strings.Contains(view, "first phase") ||
		!strings.Contains(view, "second phase · third phase") {
		t.Errorf("progressModel.Update(spinner tick).View() = %q, want unchanged layered messages", view)
	}

	updated, _ = model.Update(progressMessagesMsg{"replacement"})
	model = updated.(progressModel)
	if view := model.View(); !strings.Contains(view, "replacement") ||
		strings.Contains(view, "second phase") || strings.Contains(view, "\n") {
		t.Errorf("progressModel.Update(replacement).View() = %q, want only one replacement line", view)
	}

	updated, cmd := model.Update(progressStopMsg{})
	model = updated.(progressModel)
	if view := model.View(); view != "" || cmd == nil {
		t.Errorf("progressModel.Update(stop) = view %q, cmd %T; want empty view and tea.Quit", view, cmd)
	}
	if msg := cmd(); msg != tea.Quit() {
		t.Errorf("progressModel.Update(stop) command = %T, want tea.QuitMsg", msg)
	}
}
