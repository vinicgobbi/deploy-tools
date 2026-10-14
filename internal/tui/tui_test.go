package tui

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vinicgobbi/deploy-tools/internal/ui"
)

// TestFormInPanel drives the real bubbletea program: esc cancels a form,
// an action's huh form is answered in the panel, and its output (and that
// of the extra writers) goes to the panel, not to the terminal.
func TestFormInPanel(t *testing.T) {
	var out bytes.Buffer
	var logOut io.Writer = &out
	u := &ui.UI{Out: &out, Interactive: true}
	var name string
	var cancelErr error
	app := App{
		Title: "teste",
		Sections: []Section{{Title: "Ações", Items: []Item{
			{Label: "Cancelar", Desc: func() string { return "descrição viva" }, Run: func() error {
				_, cancelErr = u.Confirm("Seguir?", "")
				return cancelErr
			}},
			{Label: "Perguntar", Run: func() error {
				if err := u.Input("Nome", "", &name, nil); err != nil {
					return err
				}
				u.OK("olá %s", name)
				_, _ = io.WriteString(logOut, "log do provisioner\n")
				return nil
			}},
		}}},
	}
	ready := func(p *tea.Program) {
		key := func(k tea.KeyType, runes ...rune) {
			p.Send(tea.KeyMsg{Type: k, Runes: runes})
			time.Sleep(50 * time.Millisecond)
		}
		p.Send(tea.WindowSizeMsg{Width: 100, Height: 30})
		key(tea.KeyEnter) // Cancelar: the confirm opens in the panel
		key(tea.KeyEsc)   // cancels it
		key(tea.KeyEnter) // back to the menu
		key(tea.KeyDown)
		key(tea.KeyEnter) // Perguntar
		key(tea.KeyRunes, []rune("Ana")...)
		key(tea.KeyEnter) // submit
		key(tea.KeyEnter) // back to the menu
		key(tea.KeyRunes, 'q')
	}
	m, err := run(u, app, ready,
		[]tea.ProgramOption{tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutSignalHandler()}, &logOut)
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(cancelErr, ui.ErrAborted) {
		t.Fatalf("esc: err = %v", cancelErr)
	}
	if name != "Ana" {
		t.Fatalf("name = %q", name)
	}
	if log := m.log.String(); !strings.Contains(log, "olá Ana") || !strings.Contains(log, "log do provisioner") {
		t.Fatalf("painel:\n%s", log)
	}
	if out.Len() != 0 {
		t.Fatalf("saída vazou para o terminal: %q", out.String())
	}
	if u.Out != &out || u.Host != nil || logOut != &out {
		t.Fatal("saídas não restauradas")
	}
	if v := m.View(); !strings.Contains(v, "AÇÕES") || !strings.Contains(v, "Perguntar") {
		t.Fatalf("view:\n%s", v)
	}
}

func TestWrapKeepsHangingIndent(t *testing.T) {
	got := wrap("  1. O app precisa existir no servidor antes do deploy", 24)
	want := "  1. O app precisa\n     existir no servidor\n     antes do deploy"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}
