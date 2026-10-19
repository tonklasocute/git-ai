// Package ui renders terminal output: styled status lines that degrade to
// plain text (NO_COLOR, TERM=dumb, pipes), a spinner, and the interactive
// commit-message picker.
package ui

import (
	"fmt"
	"io"
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"golang.org/x/term"
)

// IsTerminal reports whether v is an *os.File attached to a terminal.
func IsTerminal(v any) bool {
	f, ok := v.(*os.File)
	return ok && term.IsTerminal(int(f.Fd())) //nolint:gosec // fd fits in int
}

// ColorEnabled reports whether w should get ANSI styling.
func ColorEnabled(w io.Writer) bool {
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	return IsTerminal(w)
}

// Printer writes status lines to w (normally stderr, so stdout stays clean
// for piping the generated text).
type Printer struct {
	w      io.Writer
	r      *lipgloss.Renderer
	Color  bool
	styles Styles
}

// Styles used across commands.
type Styles struct {
	Warn, Info, Success, Dim, Bold, Title, Box, Key lipgloss.Style
	High, Medium, Low, Note                         lipgloss.Style
}

// NewPrinter returns a Printer for w.
func NewPrinter(w io.Writer) *Printer {
	color := ColorEnabled(w)
	r := lipgloss.NewRenderer(w)
	if !color {
		r.SetColorProfile(termenv.Ascii)
	}
	p := &Printer{w: w, r: r, Color: color}
	p.styles = Styles{
		Warn:    r.NewStyle().Foreground(lipgloss.Color("214")).Bold(true),
		Info:    r.NewStyle().Foreground(lipgloss.Color("39")),
		Success: r.NewStyle().Foreground(lipgloss.Color("42")).Bold(true),
		Dim:     r.NewStyle().Foreground(lipgloss.Color("245")),
		Bold:    r.NewStyle().Bold(true),
		Title:   r.NewStyle().Bold(true).Foreground(lipgloss.Color("212")),
		Box:     r.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("63")).Padding(0, 1),
		Key:     r.NewStyle().Bold(true).Foreground(lipgloss.Color("212")),
		High:    r.NewStyle().Bold(true).Foreground(lipgloss.Color("196")),
		Medium:  r.NewStyle().Bold(true).Foreground(lipgloss.Color("214")),
		Low:     r.NewStyle().Foreground(lipgloss.Color("39")),
		Note:    r.NewStyle().Foreground(lipgloss.Color("245")),
	}
	if !color {
		p.styles.Box = r.NewStyle().Border(lipgloss.NormalBorder()).Padding(0, 1)
	}
	return p
}

// Styles returns the printer's styles.
func (p *Printer) Styles() Styles { return p.styles }

// Writer returns the underlying writer.
func (p *Printer) Writer() io.Writer { return p.w }

func (p *Printer) line(style lipgloss.Style, icon, format string, args ...any) {
	fmt.Fprintln(p.w, style.Render(icon)+" "+fmt.Sprintf(format, args...))
}

// Warn prints a warning.
func (p *Printer) Warn(format string, args ...any) { p.line(p.styles.Warn, "⚠", format, args...) }

// Info prints an informational line.
func (p *Printer) Info(format string, args ...any) { p.line(p.styles.Info, "ℹ", format, args...) }

// Success prints a success line.
func (p *Printer) Success(format string, args ...any) {
	p.line(p.styles.Success, "✓", format, args...)
}

// Dim prints a de-emphasised line (verbose details).
func (p *Printer) Dim(format string, args ...any) {
	fmt.Fprintln(p.w, p.styles.Dim.Render(fmt.Sprintf(format, args...)))
}

// Println prints a plain line.
func (p *Printer) Println(s string) { fmt.Fprintln(p.w, s) }
