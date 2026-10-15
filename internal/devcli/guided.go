package devcli

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/vinicgobbi/deploy-tools/internal/project"
	"github.com/vinicgobbi/deploy-tools/internal/tui"
)

var isInstalled = installed // replaced in tests

// guided is `deploy-dev` with no arguments: the full-screen TUI (a
// line-based menu in accessible mode) over the same init/check/install.
func guided(env *Env, dir string) error {
	return tui.Run(env.UI, devApp(env, dir))
}

func devApp(env *Env, dir string) tui.App {
	hasWorkflow := func() bool {
		_, err := os.Stat(workflowPath(dir))
		return err == nil
	}
	app := tui.App{
		Title: "deploy-dev " + env.Version,
		Subtitle: func() string {
			abs, _ := filepath.Abs(dir)
			s := filepath.Base(abs)
			if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
				s += " (não é a raiz de um repositório git)"
			}
			return s
		},
		Sections: []tui.Section{
			{Title: "Projeto", Items: []tui.Item{
				{
					Label: "Gerar workflow",
					Run:   func() error { return runInit(env, &initFlags{dir: dir}) },
					Desc: func() string {
						o := project.Detect(dir)
						s := "Gera o " + workflowPath(dir) + " no padrão da organização. "
						if hasWorkflow() {
							s += "O arquivo já existe: antes de sobrescrever, eu pergunto."
						} else {
							s += "O projeto ainda não tem workflow de deploy."
						}
						return s + "\n\nDetectei:\n" + tui.Capture(env.UI, func() { env.UI.KV(describe(o)...) }) +
							"\n\nAo abrir, você confirma ou ajusta antes de gravar."
					},
					CLI: "deploy-dev init",
				},
				{
					Label: "Checar workflows",
					Run: func() error {
						// The findings are already in the output; no extra error line.
						if err := runCheck(env, dir, false); !errors.Is(err, errFindings) {
							return err
						}
						return nil
					},
					Desc: func() string {
						s := "Valida os workflows de .github/workflows: actions fixadas por SHA (com a versão comentada), " +
							"formato do release id, passo de SSH que pode virar a action de deploy, restos de Gitea " +
							"(contexto gitea.*, URL em uses:) e proteção contra loop do bump."
						if !hasWorkflow() {
							s += "\n\nAinda não há deploy.yml: gere primeiro."
						}
						return s
					},
					CLI: "deploy-dev check",
				},
			}},
		},
	}
	// Called by name, it is the installed binary (kept up to date by the
	// automatic update): installing makes sense only for a loose copy.
	if !env.FromPATH {
		app.Sections = append(app.Sections, tui.Section{Title: "Instalação", Items: []tui.Item{
			{
				Label: "Instalar no PATH",
				Run:   func() error { return runInstall(env, "", false, false) },
				Desc: func() string {
					s := "Copia este deploy-dev para a pasta de programas do seu usuário (sem admin) e garante " +
						"que ela está no PATH, para chamar o deploy-dev de qualquer pasta. Rodar de novo com " +
						"uma versão mais nova atualiza."
					if isInstalled() {
						s += "\n\nEste binário já é o instalado."
					}
					return s
				},
				CLI: "deploy-dev install",
			},
		}})
	}
	return app
}
