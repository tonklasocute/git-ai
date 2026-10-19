package ui

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

// GenerateFunc produces commit-message candidates, streaming raw text
// through onDelta.
type GenerateFunc func(ctx context.Context, onDelta func(string)) ([]string, error)

type pickState int

const (
	stateGenerating pickState = iota
	stateReview
	stateChoose
	stateError
)

type (
	deltaMsg   string
	genDoneMsg struct {
		cands []string
		err   error
	}
	editDoneMsg struct {
		text string
		err  error
	}
)

// CommitPicker is the interactive accept/edit/regenerate/choose/quit flow.
type CommitPicker struct {
	Generate GenerateFunc
	// Editor is the command used for [e]dit (from `git var GIT_EDITOR`).
	Editor string
	// Initial candidates skip the first generation (used by tests).
	Initial []string

	printer *Printer
	state   pickState
	spin    spinner.Model
	stream  strings.Builder
	cands   []string
	idx     int
	cursor  int
	err     error
	note    string
	ctx     context.Context
	cancel  context.CancelFunc
	send    func(tea.Msg)

	// Accepted and Message hold the outcome after Run.
	Accepted bool
	Message  string
	quit     bool
}

// NewCommitPicker builds a picker that renders with p's styles.
func NewCommitPicker(p *Printer, gen GenerateFunc, editor string) *CommitPicker {
	return &CommitPicker{Generate: gen, Editor: editor, printer: p, spin: spinner.New(spinner.WithSpinner(spinner.Dot))}
}

// Run shows the picker on out, reading keys from in.
func (m *CommitPicker) Run(ctx context.Context, in io.Reader, out io.Writer) error {
	m.ctx = ctx
	prog := tea.NewProgram(m, tea.WithInput(in), tea.WithOutput(out))
	m.send = prog.Send
	_, err := prog.Run()
	if m.cancel != nil {
		m.cancel()
	}
	return err
}

func (m *CommitPicker) Init() tea.Cmd {
	if len(m.Initial) > 0 {
		m.cands, m.state = m.Initial, stateReview
		return nil
	}
	return tea.Batch(m.spin.Tick, m.start())
}

func (m *CommitPicker) start() tea.Cmd {
	m.state, m.err, m.note = stateGenerating, nil, ""
	m.stream.Reset()
	if m.ctx == nil {
		m.ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.cancel = cancel
	send := m.send
	return func() tea.Msg {
		cands, err := m.Generate(ctx, func(s string) {
			if send != nil {
				send(deltaMsg(s))
			}
		})
		return genDoneMsg{cands: cands, err: err}
	}
}

func (m *CommitPicker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case deltaMsg:
		m.stream.WriteString(string(msg))
		return m, nil
	case genDoneMsg:
		if msg.err != nil || len(msg.cands) == 0 {
			m.state, m.err = stateError, msg.err
			if m.err == nil {
				m.err = fmt.Errorf("the model returned an empty message")
			}
			return m, nil
		}
		m.cands, m.idx, m.cursor, m.state = msg.cands, 0, 0, stateReview
		return m, nil
	case editDoneMsg:
		if msg.err != nil {
			m.note = "editor failed: " + msg.err.Error()
		} else if strings.TrimSpace(msg.text) == "" {
			m.note = "empty message after editing; kept the previous one"
		} else {
			m.cands[m.idx] = strings.TrimSpace(msg.text)
			m.note = "edited"
		}
		return m, nil
	case tea.KeyMsg:
		return m.key(msg)
	case spinner.TickMsg:
		if m.state != stateGenerating {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *CommitPicker) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := k.String()
	if s == "ctrl+c" {
		m.quit = true
		return m, tea.Quit
	}
	switch m.state {
	case stateGenerating:
		if s == "q" || s == "esc" {
			m.quit = true
			return m, tea.Quit
		}
	case stateError:
		switch s {
		case "r":
			return m, tea.Batch(m.spin.Tick, m.start())
		case "q", "esc":
			m.quit = true
			return m, tea.Quit
		}
	case stateReview:
		switch s {
		case "a", "enter":
			m.Accepted, m.Message = true, m.cands[m.idx]
			return m, tea.Quit
		case "e":
			return m, m.edit()
		case "r":
			return m, tea.Batch(m.spin.Tick, m.start())
		case "c":
			if len(m.cands) > 1 {
				m.state, m.cursor = stateChoose, m.idx
			} else {
				m.note = "only one candidate; run with -n 3 to get alternatives"
			}
		case "q", "esc":
			m.quit = true
			return m, tea.Quit
		}
	case stateChoose:
		switch s {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j", "tab":
			if m.cursor < len(m.cands)-1 {
				m.cursor++
			}
		case "enter", " ":
			m.idx, m.state, m.note = m.cursor, stateReview, ""
		case "esc", "q":
			m.state = stateReview
		default:
			if len(s) == 1 && s[0] >= '1' && s[0] <= '9' {
				if n := int(s[0] - '1'); n < len(m.cands) {
					m.idx, m.cursor, m.state = n, n, stateReview
				}
			}
		}
	}
	return m, nil
}

func (m *CommitPicker) edit() tea.Cmd {
	f, err := os.CreateTemp("", "COMMIT_EDITMSG-*.txt")
	if err != nil {
		return func() tea.Msg { return editDoneMsg{err: err} }
	}
	_, _ = f.WriteString(m.cands[m.idx] + "\n")
	_ = f.Close()
	cmd := EditorCommand(m.Editor, f.Name())
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		defer os.Remove(f.Name())
		if err != nil {
			return editDoneMsg{err: err}
		}
		b, rerr := os.ReadFile(f.Name())
		return editDoneMsg{text: stripComments(string(b)), err: rerr}
	})
}

// EditorCommand builds the command for an editor string that may contain
// arguments ("code --wait"), the way git runs it.
func EditorCommand(editor, file string) *exec.Cmd {
	if strings.TrimSpace(editor) == "" {
		editor = "vi"
		if runtime.GOOS == "windows" {
			editor = "notepad"
		}
	}
	if runtime.GOOS == "windows" {
		parts := strings.Fields(editor)
		return exec.Command(parts[0], append(parts[1:], file)...)
	}
	return exec.Command("sh", "-c", editor+` "$1"`, editor, file)
}

func stripComments(s string) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, "#") {
			continue
		}
		out = append(out, l)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func (m *CommitPicker) View() string {
	st := m.printer.styles
	var b strings.Builder
	switch m.state {
	case stateGenerating:
		b.WriteString(m.spin.View() + " " + st.Dim.Render("generating commit message… (q to cancel)") + "\n")
		if txt := strings.TrimSpace(m.stream.String()); txt != "" {
			lines := strings.Split(txt, "\n")
			if len(lines) > 12 {
				lines = lines[len(lines)-12:]
			}
			b.WriteString(st.Dim.Render(strings.Join(lines, "\n")) + "\n")
		}
	case stateError:
		b.WriteString(st.Warn.Render("✗ ") + m.err.Error() + "\n\n")
		b.WriteString(m.keys([][2]string{{"r", "retry"}, {"q", "quit"}}) + "\n")
	case stateReview:
		if m.Accepted || m.quit {
			return ""
		}
		title := "Suggested commit message"
		if len(m.cands) > 1 {
			title += fmt.Sprintf(" (%d/%d)", m.idx+1, len(m.cands))
		}
		b.WriteString(st.Title.Render(title) + "\n")
		b.WriteString(st.Box.Render(m.cands[m.idx]) + "\n")
		if m.note != "" {
			b.WriteString(st.Dim.Render(m.note) + "\n")
		}
		keys := [][2]string{{"a", "accept"}, {"e", "edit"}, {"r", "regenerate"}}
		if len(m.cands) > 1 {
			keys = append(keys, [2]string{"c", "choose"})
		}
		keys = append(keys, [2]string{"q", "quit"})
		b.WriteString(m.keys(keys) + "\n")
	case stateChoose:
		b.WriteString(st.Title.Render("Choose a commit message") + st.Dim.Render("  ↑/↓ move · enter select · 1-9 jump · esc back") + "\n\n")
		for i, c := range m.cands {
			subject := strings.SplitN(c, "\n", 2)[0]
			if i == m.cursor {
				b.WriteString(st.Key.Render(fmt.Sprintf("▸ %d. %s", i+1, subject)) + "\n")
			} else {
				fmt.Fprintf(&b, "  %d. %s\n", i+1, subject)
			}
		}
		b.WriteString("\n" + st.Box.Render(m.cands[m.cursor]) + "\n")
	}
	return b.String()
}

func (m *CommitPicker) keys(ks [][2]string) string {
	st := m.printer.styles
	parts := make([]string, len(ks))
	for i, k := range ks {
		parts[i] = st.Key.Render("["+k[0]+"]") + k[1]
	}
	return strings.Join(parts, "  ")
}
