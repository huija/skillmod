// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package ui

import (
	"io"
	"strings"
	"sync"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Progress displays a replaceable activity indicator and its detail states.
type Progress interface {
	Set(messages ...string)
	Stop()
}

// AnimatedProgress returns a Bubble Tea progress block with a primary state
// followed by any remaining states on a muted detail line.
func AnimatedProgress(out io.Writer) Progress {
	return &animatedProgress{out: out}
}

type animatedProgress struct {
	out io.Writer
	mu  sync.Mutex

	program *tea.Program
	done    chan struct{}
}

func (p *animatedProgress) Set(messages ...string) {
	messages = cleanMessages(messages)
	if len(messages) == 0 {
		p.Stop()
		return
	}

	p.mu.Lock()
	if p.program != nil {
		program := p.program
		p.mu.Unlock()
		program.Send(progressMessagesMsg(messages))
		return
	}
	program := tea.NewProgram(
		newProgressModel(messages),
		tea.WithInput(nil),
		tea.WithOutput(p.out),
		tea.WithoutSignalHandler(),
	)
	done := make(chan struct{})
	p.program = program
	p.done = done
	p.mu.Unlock()

	go func() {
		_, _ = program.Run()
		close(done)
	}()
}

func (p *animatedProgress) Stop() {
	p.mu.Lock()
	program, done := p.program, p.done
	if program == nil {
		p.mu.Unlock()
		return
	}
	p.program = nil
	p.done = nil
	p.mu.Unlock()

	program.Send(progressStopMsg{})
	<-done
}

type progressMessagesMsg []string
type progressStopMsg struct{}

type progressModel struct {
	spinner  spinner.Model
	messages []string
	stopped  bool
}

func newProgressModel(messages []string) progressModel {
	return progressModel{
		spinner: spinner.New(
			spinner.WithSpinner(spinner.MiniDot),
			spinner.WithStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("212"))),
		),
		messages: messages,
	}
}

func (m progressModel) Init() tea.Cmd {
	return m.spinner.Tick
}

func (m progressModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case progressMessagesMsg:
		m.messages = cleanMessages(msg)
		return m, nil
	case progressStopMsg:
		m.stopped = true
		return m, tea.Quit
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	default:
		return m, nil
	}
}

func (m progressModel) View() string {
	if m.stopped || len(m.messages) == 0 {
		return ""
	}
	indicator := m.spinner.View()
	primary := lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Render(m.messages[0])
	view := indicator + " " + primary
	if len(m.messages) == 1 {
		return view
	}
	details := lipgloss.NewStyle().Foreground(lipgloss.Color("246")).Render(strings.Join(m.messages[1:], " · "))
	indent := strings.Repeat(" ", lipgloss.Width(indicator)+1)
	return view + "\n" + indent + details
}

func cleanMessages(messages []string) []string {
	cleaned := make([]string, 0, len(messages))
	for _, message := range messages {
		if message = cleanLine(message); message != "" {
			cleaned = append(cleaned, message)
		}
	}
	return cleaned
}
