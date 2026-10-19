package ui

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

func TestPrinterPlain(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	var buf bytes.Buffer
	p := NewPrinter(&buf)
	if p.Color || ColorEnabled(&buf) {
		t.Error("buffer must not get colour")
	}
	p.Warn("careful %d", 1)
	p.Info("note")
	p.Success("done")
	p.Dim("quiet")
	p.Println("plain")
	want := "⚠ careful 1\nℹ note\n✓ done\nquiet\nplain\n"
	if buf.String() != want {
		t.Errorf("output = %q", buf.String())
	}
	if strings.Contains(buf.String(), "\x1b[") {
		t.Error("ANSI codes in plain output")
	}
	if p.Writer() != &buf {
		t.Error("Writer")
	}
	_ = p.Styles()
	t.Setenv("TERM", "dumb")
	if ColorEnabled(os.Stderr) {
		t.Error("TERM=dumb")
	}
	if IsTerminal(&buf) {
		t.Error("buffer is not a terminal")
	}
}

func TestSpinNonInteractive(t *testing.T) {
	var buf bytes.Buffer
	called := false
	err := Spin(context.Background(), &buf, "x", func(ctx context.Context, update func(string)) error {
		update("ignored")
		called = true
		return errors.New("boom")
	})
	if !called || err == nil || err.Error() != "boom" || buf.Len() != 0 {
		t.Errorf("called=%v err=%v out=%q", called, err, buf.String())
	}
}

func TestSpinModel(t *testing.T) {
	cancelled := false
	m := spinModel{sp: spinner.New(), title: "working", cancel: func() { cancelled = true }}
	if m.Init() == nil {
		t.Error("Init should tick")
	}
	nm, _ := m.Update(spinTitle("step 2"))
	if !strings.Contains(nm.View(), "step 2") {
		t.Errorf("view = %q", nm.View())
	}
	nm, _ = nm.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if !cancelled {
		t.Error("ctrl+c should cancel")
	}
	nm, _ = nm.Update(nm.(spinModel).sp.Tick())
	nm, cmd := nm.Update(spinDone{err: errors.New("x")})
	if cmd == nil || nm.View() != "" || nm.(spinModel).err == nil {
		t.Error("done handling")
	}
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func newPicker(t *testing.T, gen GenerateFunc) *CommitPicker {
	t.Helper()
	t.Setenv("NO_COLOR", "1")
	return NewCommitPicker(NewPrinter(&bytes.Buffer{}), gen, "true")
}

// drive runs a tea.Cmd chain synchronously, feeding messages back.
func drive(m *CommitPicker, cmd tea.Cmd) {
	for cmd != nil {
		msg := cmd()
		if b, ok := msg.(tea.BatchMsg); ok {
			for _, c := range b {
				if c == nil {
					continue
				}
				if r := c(); r != nil {
					if _, isTick := r.(spinner.TickMsg); isTick {
						m.Update(r)
						continue
					}
					_, cmd = m.Update(r)
					drive(m, cmd)
				}
			}
			return
		}
		_, cmd = m.Update(msg)
	}
}

func TestPickerFlow(t *testing.T) {
	calls := 0
	gen := func(ctx context.Context, onDelta func(string)) ([]string, error) {
		calls++
		if onDelta != nil {
			onDelta("feat: x")
		}
		if calls == 1 {
			return []string{"feat: one", "feat: two\n\n- body", "feat: three"}, nil
		}
		return []string{"fix: regenerated"}, nil
	}
	m := newPicker(t, gen)
	drive(m, m.Init())
	if m.state != stateReview || len(m.cands) != 3 {
		t.Fatalf("after generation: state=%v cands=%v", m.state, m.cands)
	}
	if v := m.View(); !strings.Contains(v, "Suggested commit message (1/3)") || !strings.Contains(v, "[c]choose") {
		t.Errorf("review view:\n%s", v)
	}

	m.Update(key("c"))
	if m.state != stateChoose {
		t.Fatal("c should open chooser")
	}
	m.Update(key("down"))
	m.Update(key("down"))
	m.Update(key("down")) // clamp
	m.Update(key("up"))
	if v := m.View(); !strings.Contains(v, "▸ 2. feat: two") {
		t.Errorf("chooser view:\n%s", v)
	}
	m.Update(key("enter"))
	if m.state != stateReview || m.idx != 1 {
		t.Fatalf("enter should select: idx=%d", m.idx)
	}
	m.Update(key("c"))
	m.Update(key("3"))
	if m.idx != 2 || m.state != stateReview {
		t.Errorf("number jump: idx=%d", m.idx)
	}
	m.Update(key("c"))
	m.Update(key("esc"))
	if m.state != stateReview || m.idx != 2 {
		t.Error("esc keeps selection")
	}

	// regenerate
	_, cmd := m.Update(key("r"))
	if m.state != stateGenerating || !strings.Contains(m.View(), "generating") {
		t.Fatal("r should regenerate")
	}
	drive(m, cmd)
	if len(m.cands) != 1 || m.cands[0] != "fix: regenerated" {
		t.Fatalf("regenerated cands = %v", m.cands)
	}
	m.Update(key("c"))
	if !strings.Contains(m.note, "only one candidate") {
		t.Error("c with one candidate should explain")
	}

	// edit result handling
	m.Update(editDoneMsg{text: "fix: edited by hand"})
	m.Update(editDoneMsg{text: "   "})
	m.Update(editDoneMsg{err: errors.New("no editor")})
	if m.cands[0] != "fix: edited by hand" || !strings.Contains(m.note, "editor failed") {
		t.Errorf("edit handling: %v %q", m.cands, m.note)
	}

	_, cmd = m.Update(key("a"))
	if !m.Accepted || m.Message != "fix: edited by hand" || cmd == nil {
		t.Error("accept")
	}
	if m.View() != "" {
		t.Error("view should clear after accept")
	}
}

func TestPickerErrorRetryQuit(t *testing.T) {
	fail := true
	m := newPicker(t, func(ctx context.Context, onDelta func(string)) ([]string, error) {
		if fail {
			return nil, errors.New("rate limited")
		}
		return nil, nil // empty → error too
	})
	drive(m, m.Init())
	if m.state != stateError || !strings.Contains(m.View(), "rate limited") {
		t.Fatalf("state=%v view=%s", m.state, m.View())
	}
	fail = false
	_, cmd := m.Update(key("r"))
	drive(m, cmd)
	if m.state != stateError || !strings.Contains(m.err.Error(), "empty") {
		t.Errorf("empty result should be an error: %v", m.err)
	}
	_, cmd = m.Update(key("q"))
	if cmd == nil || !m.quit || m.Accepted {
		t.Error("quit")
	}

	m2 := newPicker(t, nil)
	m2.Initial = []string{"a"}
	m2.Init()
	m2.Update(key("q"))
	if !m2.quit {
		t.Error("q in review")
	}
	m3 := newPicker(t, nil)
	m3.state = stateGenerating
	m3.Update(deltaMsg("partial "))
	m3.Update(deltaMsg("text"))
	if !strings.Contains(m3.View(), "partial text") {
		t.Error("streamed text not shown")
	}
	m3.Update(spinner.TickMsg{})
	m3.Update(key("q"))
	m4 := newPicker(t, nil)
	m4.Update(key("ctrl+c"))
	if !m4.quit {
		t.Error("ctrl+c")
	}
	m4.Update(spinner.TickMsg{}) // ignored outside generating
}

func TestEditorCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh-based editor test")
	}
	f := filepath.Join(t.TempDir(), "msg.txt")
	_ = os.WriteFile(f, []byte("old\n"), 0o644)
	cmd := EditorCommand(`printf '# comment\nfeat: new\n' >`, f)
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(f)
	if stripComments(string(b)) != "feat: new" {
		t.Errorf("edited = %q", b)
	}
	if c := EditorCommand("", f); !strings.Contains(strings.Join(c.Args, " "), "vi") {
		t.Errorf("default editor args = %v", c.Args)
	}
	m := newPicker(t, nil)
	m.Initial = []string{"x"}
	m.Init()
	if _, cmd := m.Update(key("e")); cmd == nil {
		t.Error("e should exec the editor")
	}
}

func TestRunPicker(t *testing.T) {
	m := newPicker(t, nil)
	m.Initial = []string{"feat: piped"}
	var out bytes.Buffer
	if err := m.Run(context.Background(), strings.NewReader("a"), &out); err != nil {
		t.Fatal(err)
	}
	if !m.Accepted || m.Message != "feat: piped" {
		t.Errorf("accepted=%v msg=%q", m.Accepted, m.Message)
	}
}
