// Package ui wraps prompts (huh) and styled output for the manual CLI. The
// CI forced command never uses it.
package ui

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"golang.org/x/term"
)

type UI struct {
	In          io.Reader
	Out         io.Writer
	Interactive bool // prompts allowed (stdin and stdout are terminals)
	Accessible  bool // plain line-based prompts (tests, screen readers)
	ClearScreen bool // Clear() actually clears (only on a real terminal)
	// Host, when set, shows the forms instead of running them on the
	// terminal (the full-screen TUI embeds them in its panel).
	Host Host
}

// Host runs a form inside another program; it returns ErrAborted when the
// user cancels.
type Host interface {
	RunForm(*huh.Form) error
}

var ErrAborted = errors.New("cancelado")

func New() *UI {
	interactive := term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
	if os.Getenv("NO_COLOR") != "" || !term.IsTerminal(int(os.Stdout.Fd())) {
		lipgloss.SetColorProfile(termenv.Ascii)
	}
	accessible := os.Getenv("ACCESSIBLE") != ""
	return &UI{
		In: os.Stdin, Out: os.Stdout, Interactive: interactive, Accessible: accessible,
		// Screen readers lose context when the screen is wiped.
		ClearScreen: interactive && !accessible,
	}
}

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	okStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	warnStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	errStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9"))
	faintStyle = lipgloss.NewStyle().Faint(true)
	keyStyle   = lipgloss.NewStyle().Bold(true)
)

func (u *UI) Title(s string) { fmt.Fprintln(u.Out, titleStyle.Render(s)) }

// Header opens a screen: the title, then each non-empty line faint, then a
// blank line.
func (u *UI) Header(title string, lines ...string) {
	u.Title(title)
	for _, l := range lines {
		if l != "" {
			fmt.Fprintln(u.Out, faintStyle.Render(l))
		}
	}
	fmt.Fprintln(u.Out)
}

// Tone colors a short status text (no line break).
type Tone int

const (
	Plain Tone = iota
	Good
	Idle
	Bad
)

func (u *UI) Tone(t Tone, s string) string {
	switch t {
	case Good:
		return okStyle.Render(s)
	case Idle:
		return faintStyle.Render(s)
	case Bad:
		return warnStyle.Render(s)
	}
	return s
}

// Hint prints an indented, faint line: context the user can skip.
func (u *UI) Hint(format string, a ...any) {
	fmt.Fprintln(u.Out, renderLines(faintStyle, "  "+fmt.Sprintf(format, a...)))
}
func (u *UI) OK(format string, a ...any) {
	fmt.Fprintln(u.Out, renderLines(okStyle, "✓ "+fmt.Sprintf(format, a...)))
}
func (u *UI) Warn(format string, a ...any) {
	fmt.Fprintln(u.Out, renderLines(warnStyle, "! "+fmt.Sprintf(format, a...)))
}
func (u *UI) Println(a ...any)               { fmt.Fprintln(u.Out, a...) }
func (u *UI) Printf(format string, a ...any) { fmt.Fprintf(u.Out, format, a...) }
func (u *UI) Faint(s string) string          { return faintStyle.Render(s) }
func ErrorText(s string) string              { return errStyle.Render("erro: ") + s }

// renderLines styles each line on its own: rendering a multi-line string
// at once pads every line to the widest one with trailing spaces.
func renderLines(st lipgloss.Style, s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = st.Render(l)
	}
	return strings.Join(lines, "\n")
}

// clearSeq moves home, clears the screen and the scrollback (like clear(1)).
const clearSeq = "\033[H\033[2J\033[3J"

// Clear wipes the terminal so each interactive screen starts clean.
func (u *UI) Clear() {
	if u.ClearScreen {
		fmt.Fprint(u.Out, clearSeq)
	}
}

// KV prints aligned "key  value" lines.
func (u *UI) KV(pairs ...[2]string) {
	w := 0
	for _, p := range pairs {
		w = max(w, len([]rune(p[0])))
	}
	for _, p := range pairs {
		pad := strings.Repeat(" ", w-len([]rune(p[0])))
		fmt.Fprintf(u.Out, "  %s%s  %s\n", keyStyle.Render(p[0]), pad, p[1])
	}
}

// Steps prints a numbered list.
func (u *UI) Steps(title string, steps ...string) {
	if len(steps) == 0 {
		return
	}
	fmt.Fprintln(u.Out)
	u.Title(title)
	for i, s := range steps {
		fmt.Fprintf(u.Out, "  %d. %s\n", i+1, s)
	}
}

// Run shows a form, mapping ctrl+c/esc to ErrAborted.
func (u *UI) Run(groups ...*huh.Group) error {
	if !u.Interactive {
		return errors.New("é preciso um terminal interativo para perguntar (ou passe as flags)")
	}
	if u.Host != nil {
		// The host shows its own key hints.
		return u.Host.RunForm(huh.NewForm(groups...).WithTheme(huh.ThemeCharm()).WithShowHelp(false))
	}
	in := u.In
	if u.Accessible {
		// Each accessible-mode form scans its input with its own buffer;
		// handing out one line per Read keeps later forms from losing
		// answers buffered (and discarded) by earlier ones.
		if _, ok := in.(*lineReader); !ok {
			u.In = &lineReader{r: bufio.NewReader(in)}
		}
		in = u.In
	}
	err := huh.NewForm(groups...).
		WithInput(in).WithOutput(u.Out).
		WithAccessible(u.Accessible).
		WithTheme(huh.ThemeCharm()).
		Run()
	if errors.Is(err, huh.ErrUserAborted) {
		return ErrAborted
	}
	return err
}

func (u *UI) Confirm(title, description string) (bool, error) {
	return u.ConfirmDefault(title, description, false)
}

// ConfirmDefault is Confirm with def preselected (Enter accepts it).
func (u *UI) ConfirmDefault(title, description string, def bool) (bool, error) {
	ok := def
	err := u.Run(huh.NewGroup(huh.NewConfirm().Title(title).Description(description).
		Affirmative("Sim").Negative("Não").Value(&ok)))
	return ok, err
}

func (u *UI) Select(title string, val *string, opts ...huh.Option[string]) error {
	return u.Run(huh.NewGroup(huh.NewSelect[string]().Title(title).Options(opts...).Value(val)))
}

func (u *UI) Input(title, placeholder string, val *string, validate func(string) error) error {
	in := huh.NewInput().Title(title).Placeholder(placeholder).Value(val)
	if validate != nil {
		in = in.Validate(validate)
	}
	return u.Run(huh.NewGroup(in))
}

type lineReader struct{ r *bufio.Reader }

func (l *lineReader) Read(p []byte) (int, error) {
	line, err := l.r.ReadSlice('\n')
	n := copy(p, line)
	if n < len(line) {
		// p too small: put the rest back for the next Read.
		l.r = bufio.NewReader(io.MultiReader(bytes.NewReader(line[n:]), l.r))
		return n, nil
	}
	if err == bufio.ErrBufferFull {
		err = nil
	}
	return n, err
}
