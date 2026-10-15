package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/aymanbagabas/go-osc52/v2"
	"github.com/charmbracelet/huh"
)

// copyToClipboard asks the operator's terminal (not this server) to put
// text on its clipboard with OSC 52, which also works over SSH. tmux and
// screen need the sequence wrapped. There is no reply: whether it worked
// depends on the terminal.
func copyToClipboard(env *Env, text string) error {
	w := env.clipboard
	if w == nil {
		tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
		if err != nil {
			return fmt.Errorf("sem terminal para copiar: %w", err)
		}
		defer tty.Close()
		w = tty
	}
	seq := osc52.New(text)
	term := os.Getenv("TERM")
	switch {
	case os.Getenv("TMUX") != "" || strings.HasPrefix(term, "tmux"):
		seq = seq.Tmux()
	case strings.HasPrefix(term, "screen"):
		seq = seq.Screen()
	}
	_, err := seq.WriteTo(w)
	return err
}

type copyItem struct{ label, text string }

// offerCopy lets the operator copy texts that are hard to select on screen
// (in the TUI the menu is drawn beside them), or save the gh commands to a
// file. Only in the TUI; ghCommands may be empty.
func offerCopy(env *Env, ghCommands, script string, items ...copyItem) error {
	if !env.TUI || !env.UI.Interactive {
		return nil
	}
	if ghCommands != "" {
		items = append(items, copyItem{"os comandos gh secret set", ghCommands})
	}
	for {
		env.UI.Println()
		choice := "done"
		opts := make([]huh.Option[string], 0, len(items)+2)
		for i, it := range items {
			opts = append(opts, huh.NewOption("Copiar "+it.label, strconv.Itoa(i)))
		}
		if ghCommands != "" {
			opts = append(opts, huh.NewOption("Salvar um script dos secrets (sh github-secrets.sh dono/repo)", "file"))
		}
		opts = append(opts, huh.NewOption("Concluir", "done"))
		if err := env.UI.Select("Copiar para a área de transferência do seu terminal?", &choice, opts...); err != nil {
			return err
		}
		switch choice {
		case "done":
			return nil
		case "file":
			if err := saveCommands(env, script); err != nil {
				env.UI.Warn("%s", err.Error())
			}
		default:
			i, _ := strconv.Atoi(choice)
			if err := copyToClipboard(env, items[i].text); err != nil {
				env.UI.Warn("%s", err.Error())
				continue
			}
			env.UI.OK("Copiado: %s.", items[i].label)
			env.UI.Hint("%s", "Se colar não trouxer nada, seu terminal não aceita cópia por OSC 52 (ex.: tmux sem set-clipboard) — use Salvar num arquivo.")
		}
	}
}

// saveCommands writes the secrets script where the operator says.
func saveCommands(env *Env, script string) error {
	dir, _ := os.Getwd()
	def := freeScriptPath(dir)
	var path string
	if err := env.UI.Input("Salvar em", def, &path, nil); err != nil {
		return err
	}
	if path == "" {
		path = def
	}
	if err := writeScript(path, script); err != nil {
		return err
	}
	env.UI.OK("Salvo em %s (modo 600). Depois de sair: sh %s dono/repo  (precisa do gh logado)", path, path)
	return nil
}
