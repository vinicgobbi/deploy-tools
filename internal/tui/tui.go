// Package tui is the full-screen interface of deploy-tools and deploy-dev:
// the actions in a sidebar, and a panel that describes the selected one
// and then runs it. Actions are the same plain functions the commands use:
// each runs in its own goroutine, its output is redirected to the panel and
// its huh forms are shown there (see ui.Host).
//
// In accessible mode (ACCESSIBLE=1, and in tests) the same items are shown
// as a line-based menu instead.
package tui

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"

	"github.com/vinicgobbi/deploy-tools/internal/ui"
)

type Item struct {
	Label string
	// Desc is the panel text while the item is selected. It is computed
	// when the selection changes, so it can show live context.
	Desc func() string
	CLI  string // the equivalent command, shown under the description
	Root bool   // needs sudo
	Run  func() error
}

type Section struct {
	Title string
	Items []Item
}

type App struct {
	Title    string
	Subtitle func() string // context shown next to the title
	Sections []Section
	// IsRoot reports whether the user has root; nil when no item needs it.
	IsRoot func() bool
}

// Run shows app until the user quits. outs are writers besides u.Out (e.g.
// a provisioner's log) that are redirected to the panel meanwhile.
func Run(u *ui.UI, app App, outs ...*io.Writer) error {
	if u.Accessible || !u.Interactive {
		return plain(u, app)
	}
	_, err := run(u, app, nil, []tea.ProgramOption{tea.WithAltScreen()}, outs...)
	return err
}

// run is Run's full-screen part; ready (tests) gets the program to send
// keys to once it is set up.
func run(u *ui.UI, app App, ready func(*tea.Program), opts []tea.ProgramOption, outs ...*io.Writer) (*model, error) {
	m := newModel(u, app)
	p := tea.NewProgram(m, opts...)
	m.p = p

	w := panelWriter{p}
	oldOut, saved := u.Out, make([]io.Writer, len(outs))
	u.Out, u.Host = w, (*host)(m)
	for i, o := range outs {
		saved[i], *o = *o, w
	}
	defer func() {
		u.Out, u.Host = oldOut, nil
		for i, o := range outs {
			*o = saved[i]
		}
	}()
	if ready != nil {
		go ready(p)
	}
	_, err := p.Run()
	return m, err
}

// Capture runs f with u.Out going to a buffer, for descriptions built from
// the same functions that print command output.
func Capture(u *ui.UI, f func()) string {
	var b bytes.Buffer
	old := u.Out
	u.Out = &b
	defer func() { u.Out = old }()
	f()
	return strings.TrimRight(b.String(), "\n")
}

// plain is the line-based fallback: the same items in a ui.Menu.
func plain(u *ui.UI, app App) error {
	header := func() error {
		sub := ""
		if app.Subtitle != nil {
			sub = app.Subtitle()
		}
		u.Header(app.Title, sub)
		if app.IsRoot != nil && !app.IsRoot() {
			u.Warn("Sem sudo: dá para consultar, mas criar ou alterar exige rodar com sudo.")
			u.Println()
		}
		return nil
	}
	return u.Menu(header, "O que você quer fazer?", func() ([]ui.Entry, error) {
		var es []ui.Entry
		for _, s := range app.Sections {
			for _, it := range s.Items {
				es = append(es, ui.Entry{Label: it.Label, Run: it.Run})
			}
		}
		return append(es, ui.Entry{Label: "Sair"}), nil
	})
}

// ---- bubbletea model ----

type mode int

const (
	browsing mode = iota
	running
	done
)

type (
	outMsg  string
	doneMsg struct{ err error }
	formMsg struct {
		form *huh.Form
		done chan error
	}
)

type panelWriter struct{ p *tea.Program }

func (w panelWriter) Write(b []byte) (int, error) {
	w.p.Send(outMsg(string(b)))
	return len(b), nil
}

type host model

// RunForm is called from the action's goroutine and blocks until the user
// answers (or cancels) the form in the panel.
func (h *host) RunForm(f *huh.Form) error {
	done := make(chan error, 1)
	h.p.Send(formMsg{f, done})
	return <-done
}

type model struct {
	u    *ui.UI
	app  App
	p    *tea.Program
	refs [][2]int // flat index -> section, item

	cursor int
	mode   mode
	desc   string
	root   bool

	log      strings.Builder
	result   error
	form     *huh.Form
	formDone chan error
	vp       viewport.Model

	w, h int
}

func newModel(u *ui.UI, app App) *model {
	m := &model{u: u, app: app, root: app.IsRoot == nil || app.IsRoot()}
	for si, s := range app.Sections {
		for ii := range s.Items {
			m.refs = append(m.refs, [2]int{si, ii})
		}
	}
	m.vp = viewport.New(0, 0)
	m.loadDesc()
	return m
}

func (m *model) item() Item {
	r := m.refs[m.cursor]
	return m.app.Sections[r[0]].Items[r[1]]
}

func (m *model) loadDesc() {
	it := m.item()
	m.desc = ""
	if it.Desc != nil {
		m.desc = it.Desc()
	}
}

func (m *model) Init() tea.Cmd { return nil }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.layout()
		if m.form != nil {
			m.form = m.form.WithWidth(m.panelW())
			fm, cmd := m.form.Update(msg)
			m.form = fm.(*huh.Form)
			m.refresh()
			return m, cmd
		}
		m.refresh()
		return m, nil
	case outMsg:
		m.log.WriteString(string(msg))
		m.refresh()
		return m, nil
	case formMsg:
		m.form, m.formDone = msg.form.WithWidth(m.panelW()), msg.done
		cmd := m.form.Init()
		m.refresh()
		return m, cmd
	case doneMsg:
		m.mode, m.result = done, msg.err
		m.refresh()
		return m, nil
	}

	if m.form != nil {
		if k, ok := msg.(tea.KeyMsg); ok && k.String() == "esc" {
			m.finishForm(ui.ErrAborted)
			return m, nil
		}
		fm, cmd := m.form.Update(msg)
		m.form = fm.(*huh.Form)
		switch m.form.State {
		case huh.StateCompleted:
			m.finishForm(nil)
		case huh.StateAborted:
			m.finishForm(ui.ErrAborted)
		default:
			m.refresh()
		}
		return m, cmd
	}

	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch m.mode {
	case browsing:
		switch k.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			m.move(-1)
		case "down", "j":
			m.move(1)
		case "home", "g":
			m.move(-len(m.refs))
		case "end", "G":
			m.move(len(m.refs))
		case "enter", "right", "l":
			m.start()
		}
	case done:
		switch k.String() {
		case "enter", "esc", "left", "h", "q":
			m.mode = browsing
			m.loadDesc() // the action may have changed what it shows
		case "ctrl+c":
			return m, tea.Quit
		default:
			var cmd tea.Cmd
			m.vp, cmd = m.vp.Update(msg)
			return m, cmd
		}
	}
	// While running without a form, keys are ignored: quitting would kill
	// the action halfway (e.g. in the middle of a migration).
	return m, nil
}

func (m *model) move(d int) {
	m.cursor = max(0, min(len(m.refs)-1, m.cursor+d))
	m.loadDesc()
}

func (m *model) start() {
	it := m.item()
	m.mode, m.result = running, nil
	m.log.Reset()
	m.refresh()
	go func() {
		m.p.Send(doneMsg{safeRun(it.Run)})
	}()
}

func safeRun(f func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("erro interno: %v", r)
		}
	}()
	return f()
}

func (m *model) finishForm(err error) {
	m.formDone <- err
	m.form, m.formDone = nil, nil
	m.refresh()
}

// ---- layout ----

var (
	accent    = lipgloss.Color("12")
	titleSt   = lipgloss.NewStyle().Bold(true).Foreground(accent)
	faintSt   = lipgloss.NewStyle().Faint(true)
	selSt     = lipgloss.NewStyle().Bold(true).Foreground(accent)
	okSt      = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	warnSt    = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	boxSt     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("8")).Padding(0, 1)
	activeBox = boxSt.BorderForeground(accent)
)

func (m *model) sideW() int {
	w := 18
	for _, s := range m.app.Sections {
		for _, it := range s.Items {
			w = max(w, lipgloss.Width(it.Label)+4)
		}
	}
	return w
}

// panelW is the text width inside the panel box.
func (m *model) panelW() int { return max(20, m.w-m.sideW()-4-4) }

// bodyH is the height inside the boxes (header and footer take a line each).
func (m *model) bodyH() int { return max(5, m.h-2-2) }

func (m *model) layout() {
	m.vp.Width = m.panelW()
	m.vp.Height = m.bodyH() - 2 // panel title + blank line
}

// refresh rebuilds the panel content of a running/done action.
func (m *model) refresh() {
	if m.mode == browsing {
		return
	}
	var parts []string
	if out := strings.TrimRight(m.log.String(), "\n"); out != "" {
		parts = append(parts, wrap(out, m.panelW()))
	}
	if m.form != nil {
		parts = append(parts, m.form.View())
	}
	if m.mode == done {
		switch {
		case m.result == nil && len(parts) > 0:
			parts = append(parts, faintSt.Render("Concluído."))
		case m.result == nil:
			parts = append(parts, okSt.Render("✓ Concluído."))
		case m.result == ui.ErrAborted:
			parts = append(parts, warnSt.Render("Cancelado."))
		default:
			parts = append(parts, wrap(ui.ErrorText(m.result.Error()), m.panelW()))
		}
	}
	m.vp.SetContent(strings.Join(parts, "\n\n"))
	m.vp.GotoBottom()
}

// listPrefix matches the indent of a line plus a list marker ("  1. ",
// "  · "), so wrapped lines keep a hanging indent.
var listPrefix = regexp.MustCompile(`^ *(\d+\. |[·•\-] )?`)

func wrap(s string, w int) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if lipgloss.Width(l) <= w {
			continue
		}
		prefix := listPrefix.FindString(l)
		if len(prefix) > w/2 {
			prefix = ""
		}
		pad := strings.Repeat(" ", lipgloss.Width(prefix))
		body := lipgloss.NewStyle().Width(w - len(pad)).Render(l[len(prefix):])
		parts := strings.Split(body, "\n")
		for j := range parts {
			parts[j] = strings.TrimRight(parts[j], " ")
			if j == 0 {
				parts[j] = prefix + parts[j]
			} else {
				parts[j] = pad + parts[j]
			}
		}
		lines[i] = strings.Join(parts, "\n")
	}
	return strings.Join(lines, "\n")
}

func (m *model) View() string {
	if m.w == 0 {
		return ""
	}
	if m.w < 60 || m.h < 16 {
		return "Terminal pequeno demais: aumente a janela (mínimo 60×16).\n\nq sai."
	}
	return lipgloss.JoinVertical(lipgloss.Left, m.header(), m.body(), m.footer())
}

func (m *model) header() string {
	left := titleSt.Render(m.app.Title)
	if m.app.Subtitle != nil {
		if sub := m.app.Subtitle(); sub != "" {
			left += faintSt.Render("  ·  " + sub)
		}
	}
	right := ""
	if m.app.IsRoot != nil {
		right = okSt.Render("sudo ✓")
		if !m.root {
			right = warnSt.Render("sem sudo · só consulta")
		}
	}
	gap := max(1, m.w-lipgloss.Width(left)-lipgloss.Width(right)-1)
	return " " + left + strings.Repeat(" ", gap) + right
}

func (m *model) body() string {
	h := m.bodyH()
	side := m.sidebar()
	sideBox, panelBox := activeBox, boxSt
	if m.mode != browsing {
		sideBox, panelBox = boxSt, activeBox
	}
	l := sideBox.Width(m.sideW()).Height(h).Render(side)
	r := panelBox.Width(m.panelW() + 2).Height(h).Render(m.panel())
	return lipgloss.JoinHorizontal(lipgloss.Top, l, r)
}

func (m *model) sidebar() string {
	var b strings.Builder
	i := 0
	for si, s := range m.app.Sections {
		if si > 0 {
			b.WriteString("\n")
		}
		b.WriteString(faintSt.Render(strings.ToUpper(s.Title)) + "\n")
		for _, it := range s.Items {
			label := it.Label
			switch {
			case i == m.cursor && m.mode == browsing:
				label = selSt.Render("▸ " + label)
			case i == m.cursor:
				label = selSt.Render("● " + label)
			case it.Root && !m.root:
				label = faintSt.Render("  " + label)
			default:
				label = "  " + label
			}
			b.WriteString(label + "\n")
			i++
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m *model) panel() string {
	it := m.item()
	title := titleSt.Render(it.Label)
	if m.mode != browsing {
		return title + "\n\n" + m.vp.View()
	}
	var parts []string
	if m.desc != "" {
		parts = append(parts, wrap(m.desc, m.panelW()))
	}
	if it.Root && !m.root {
		parts = append(parts, warnSt.Render(wrap("Precisa de sudo. Saia (q) e abra de novo com sudo.", m.panelW())))
	}
	if it.CLI != "" {
		parts = append(parts, faintSt.Render("Pelo terminal:")+"\n"+wrap(faintSt.Render(it.CLI), m.panelW()))
	}
	body := strings.Join(parts, "\n\n")
	// Keep the description inside the box: cut what does not fit.
	if lines := strings.Split(body, "\n"); len(lines) > m.bodyH()-2 {
		body = strings.Join(lines[:m.bodyH()-3], "\n") + "\n" + faintSt.Render("…")
	}
	return title + "\n\n" + body
}

func (m *model) footer() string {
	var keys string
	switch {
	case m.form != nil:
		keys = "↑↓←→ escolher · enter confirmar · esc cancelar"
	case m.mode == running:
		keys = "executando…"
	case m.mode == done:
		keys = "enter/esc voltar ao menu · ↑↓ rolar"
	default:
		keys = "↑↓ navegar · enter abrir · q sair"
	}
	return faintSt.Render(" " + keys)
}
