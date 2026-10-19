package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

type spinDone struct{ err error }
type spinTitle string

type spinModel struct {
	sp     spinner.Model
	title  string
	err    error
	done   bool
	cancel context.CancelFunc
}

func (m spinModel) Init() tea.Cmd { return m.sp.Tick }

func (m spinModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinDone:
		m.done, m.err = true, msg.err
		return m, tea.Quit
	case spinTitle:
		m.title = string(msg)
		return m, nil
	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			m.cancel()
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.sp, cmd = m.sp.Update(msg)
	return m, cmd
}

func (m spinModel) View() string {
	if m.done {
		return ""
	}
	return m.sp.View() + " " + m.title + "\n"
}

// Spin runs fn while showing a spinner on w (when w is a colour-capable
// terminal). fn may call update to change the title. Ctrl+C cancels ctx.
func Spin(ctx context.Context, w io.Writer, title string, fn func(ctx context.Context, update func(string)) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if !ColorEnabled(w) || !IsTerminal(os.Stdin) {
		return fn(ctx, func(string) {})
	}
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	m := spinModel{sp: sp, title: title, cancel: cancel}
	p := tea.NewProgram(m, tea.WithOutput(w), tea.WithInput(os.Stdin))
	go func() {
		err := fn(ctx, func(s string) { p.Send(spinTitle(s)) })
		p.Send(spinDone{err: err})
	}()
	res, err := p.Run()
	if err != nil {
		return fmt.Errorf("spinner: %w", err)
	}
	final := res.(spinModel)
	if !final.done {
		return errors.New("interrupted")
	}
	return final.err
}
