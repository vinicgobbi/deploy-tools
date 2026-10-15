// Package devcli is the command tree of deploy-dev, the developer-desktop
// binary. It only reads and writes files of the project checkout; none of
// the server-side code (CI forced command, provisioning) is linked in.
package devcli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/vinicgobbi/deploy-tools/internal/project"
	"github.com/vinicgobbi/deploy-tools/internal/ui"
	"github.com/vinicgobbi/deploy-tools/internal/update"
)

type Env struct {
	UI *ui.UI
	// Version and Commit of this build: the generated workflow pins the
	// deploy action to Commit (the tag's commit in a release build).
	Version, Commit string

	// FromPATH: called by name, so it is the installed binary and the
	// guided menu hides "Instalar no PATH" (updates are automatic).
	FromPATH bool
	// Self-update (see update.go). Off in tests unless set.
	Updates     update.Client
	AutoUpdate  bool
	UpdateStamp string
}

func Execute(env *Env, args []string) int {
	root := NewRoot(env)
	root.SetArgs(args)
	root.SetOut(env.UI.Out)
	if err := root.Execute(); err != nil {
		if errors.Is(err, ui.ErrAborted) {
			fmt.Fprintln(os.Stderr, "Cancelado.")
			return 130
		}
		if errors.Is(err, errFindings) {
			return 1
		}
		fmt.Fprintln(os.Stderr, ui.ErrorText(err.Error()))
		return 1
	}
	return 0
}

var errFindings = errors.New("check encontrou problemas")

func NewRoot(env *Env) *cobra.Command {
	root := &cobra.Command{
		Use:   "deploy-dev",
		Short: "Configura o deploy de um projeto (lado do desenvolvedor)",
		Long: `deploy-dev gera e valida o workflow de deploy (.github/workflows/deploy.yml)
dos projetos que publicam no servidor via deploy-tools.

Roda na sua máquina, dentro do repositório do projeto. Os comandos de
servidor ficam no deploy-tools, instalado só no servidor.

Sem argumentos, num terminal, abre um modo guiado.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			switch cmd.Name() {
			case "version", "update", "install", "help", "completion", "__complete", "__completeNoDesc":
				return
			}
			autoUpdate(env)
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !env.UI.Interactive {
				return cmd.Help()
			}
			return guided(env, ".")
		},
	}
	root.AddCommand(initCmd(env), checkCmd(env), installCmd(env), updateCmd(env), &cobra.Command{
		Use:   "version",
		Short: "Mostra a versão (e a da action de deploy que o init fixa)",
		Args:  cobra.NoArgs,
		Run: func(*cobra.Command, []string) {
			env.UI.Println("deploy-dev", env.Version)
			if validRef(env.Commit) {
				env.UI.Println("action de deploy:", project.DeployToolsRepo+"@"+env.Commit)
			}
		},
	})
	return root
}

func validRef(s string) bool { return len(s) == 40 && strings.Trim(s, "0123456789abcdef") == "" }

func initCmd(env *Env) *cobra.Command {
	fl := &initFlags{}
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Gera o .github/workflows/deploy.yml do projeto",
		Long: `Detecta o tipo do projeto (Laravel, Symfony, React, Angular ou
estático/mkdocs), mostra o que encontrou para você confirmar ou ajustar e
gera o workflow de deploy para o GitHub Actions: actions fixadas por SHA,
Commitizen (se o projeto usa) e a action de deploy do deploy-tools fixada
no SHA desta versão.`,
		Example: `  deploy-dev init                 # interativo, no diretório atual
  deploy-dev init --yes           # aceita o que foi detectado
  deploy-dev init --stdout        # só mostra, não grava`,
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error { return runInit(env, fl) },
	}
	f := cmd.Flags()
	f.StringVarP(&fl.dir, "dir", "C", ".", "diretório do projeto")
	f.StringVar(&fl.app, "app", "", "nome do app no servidor (padrão: nome do repositório)")
	f.StringVarP(&fl.typ, "type", "t", "", "laravel | symfony | react | angular | static (padrão: detectado)")
	f.StringVar(&fl.branch, "branch", "", "branch que dispara o deploy (padrão: main)")
	f.BoolVarP(&fl.yes, "yes", "y", false, "não pergunta: usa o que foi detectado")
	f.BoolVar(&fl.force, "force", false, "sobrescreve um deploy.yml existente")
	f.BoolVar(&fl.stdout, "stdout", false, "imprime o workflow em vez de gravar")
	f.StringVar(&fl.actionRef, "action-ref", "", "SHA da action de deploy (padrão: o desta versão)")
	_ = f.MarkHidden("action-ref")
	return cmd
}

type initFlags struct {
	dir, app, typ, branch, actionRef string
	yes, force, stdout               bool
}

func workflowPath(dir string) string { return filepath.Join(dir, ".github", "workflows", "deploy.yml") }

func runInit(env *Env, fl *initFlags) error {
	o := project.Detect(fl.dir)
	o.ActionRef, o.ActionVersion = env.Commit, env.Version
	if fl.actionRef != "" {
		o.ActionRef = fl.actionRef
	}
	if !validRef(o.ActionRef) {
		return errors.New("este deploy-dev não foi gerado por uma release (sem SHA da action embutido) — " +
			"instale o binário da release ou passe --action-ref <sha>")
	}
	if fl.app != "" {
		o.App = fl.app
	}
	if fl.typ != "" {
		o.Type = fl.typ
	}
	if fl.branch != "" {
		o.Branch = fl.branch
	}

	interactive := env.UI.Interactive && !fl.yes && !fl.stdout
	if !interactive && !fl.yes && !fl.stdout {
		return errors.New("sem terminal: passe --yes para aceitar a detecção (ou --stdout para só visualizar)")
	}
	if interactive {
		if err := reviewOptions(env, &o); err != nil {
			return err
		}
	}
	content, err := project.Render(o)
	if err != nil {
		return err
	}
	if fl.stdout {
		env.UI.Printf("%s", content)
		return nil
	}

	path := workflowPath(fl.dir)
	if _, err := os.Stat(path); err == nil && !fl.force {
		if !interactive {
			return fmt.Errorf("%s já existe — use --force para sobrescrever (ou --stdout para comparar)", path)
		}
		ok, err := env.UI.Confirm(path+" já existe. Sobrescrever?", "Confira o diff no git depois.")
		if err != nil {
			return err
		}
		if !ok {
			return ui.ErrAborted
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return err
	}
	env.UI.OK("Gerado %s (%s, app %s)", path, typeName(o.Type), o.App)
	env.UI.Steps("Próximos passos", nextSteps(fl.dir, o)...)
	return nil
}

func typeName(t string) string {
	switch t {
	case project.Laravel:
		return "Laravel"
	case project.Symfony:
		return "Symfony"
	case project.React:
		return "React"
	case project.Angular:
		return "Angular"
	case project.Static:
		return "Estático"
	}
	return t
}

// describe is what init will generate, in the words of the questions.
func describe(o project.Options) [][2]string {
	pairs := [][2]string{
		{"app no servidor", o.App + "  (/var/www/" + o.App + ")"},
		{"tipo", typeName(o.Type)},
		{"deploy a cada push em", o.Branch},
		{"Commitizen", yesNo(o.Commitizen)},
	}
	switch o.Type {
	case project.Laravel:
		pairs = append(pairs,
			[2]string{"PHP", o.PHPVersion},
			[2]string{"Pint", yesNo(o.Pint)},
			[2]string{"assets do Vite", yesNo(o.ViteAssets)})
	case project.Symfony:
		pairs = append(pairs,
			[2]string{"PHP", o.PHPVersion},
			[2]string{"AssetMapper", yesNo(o.AssetMapper)},
			[2]string{"assets com Node (Encore/Vite)", yesNo(o.ViteAssets)})
	case project.React, project.Angular:
		pairs = append(pairs,
			[2]string{"Node", o.NodeVersion + " · " + o.PackageManager},
			[2]string{"lint antes do build", yesNo(o.Lint)})
		if o.Type == project.Angular {
			pairs = append(pairs, [2]string{"pasta publicada", o.OutputDir})
		}
	case project.Static:
		if o.MkDocs {
			pairs = append(pairs, [2]string{"build", "mkdocs"})
		} else {
			build := o.BuildCommand
			if build == "" {
				build = "nenhum (conteúdo já pronto)"
			}
			pairs = append(pairs, [2]string{"build", build}, [2]string{"pasta publicada", o.OutputDir})
		}
	}
	if len(o.ViteVars) > 0 {
		pairs = append(pairs, [2]string{"variáveis do build", strings.Join(o.ViteVars, ", ")})
	}
	return pairs
}

func yesNo(b bool) string { return map[bool]string{true: "sim", false: "não"}[b] }

// reviewOptions shows what was detected and lets the user accept it as is
// (the common case) or go through every question.
func reviewOptions(env *Env, o *project.Options) error {
	env.UI.Title("Detectei isto no projeto")
	env.UI.KV(describe(*o)...)
	env.UI.Println()
	choice := "ok"
	if err := env.UI.Select("Gerar o workflow assim?", &choice,
		huh.NewOption("Sim, gerar com estas opções", "ok"),
		huh.NewOption("Ajustar antes de gerar", "edit"),
		huh.NewOption("Cancelar", "cancel"),
	); err != nil {
		return err
	}
	switch choice {
	case "cancel":
		return ui.ErrAborted
	case "edit":
		return askOptions(env, o)
	}
	return nil
}

func askOptions(env *Env, o *project.Options) error {
	typeOpts := []huh.Option[string]{
		huh.NewOption("Laravel", project.Laravel),
		huh.NewOption("Symfony", project.Symfony),
		huh.NewOption("React (Vite)", project.React),
		huh.NewOption("Angular", project.Angular),
		huh.NewOption("Estático (mkdocs ou pasta pronta)", project.Static),
	}
	if err := env.UI.Run(huh.NewGroup(
		huh.NewInput().Title("Nome do app no servidor").Description("/var/www/<app>").Value(&o.App).
			Validate(func(s string) error {
				if s == "" || strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-") != "" {
					return errors.New("use só letras, números, - e _")
				}
				return nil
			}),
		huh.NewSelect[string]().Title("Tipo (detectado: "+o.Type+")").Options(typeOpts...).Value(&o.Type),
		huh.NewInput().Title("Branch que dispara o deploy").Value(&o.Branch),
		huh.NewConfirm().Title("Versionar com Commitizen (bump + release no GitHub)?").
			Affirmative("Sim").Negative("Não").Value(&o.Commitizen),
	)); err != nil {
		return err
	}

	var fields []huh.Field
	switch o.Type {
	case project.Laravel:
		fields = append(fields,
			huh.NewInput().Title("Versão do PHP").Value(&o.PHPVersion),
			huh.NewConfirm().Title("Checar estilo com Pint?").Affirmative("Sim").Negative("Não").Value(&o.Pint),
			huh.NewConfirm().Title("Buildar assets do Vite (public/build)?").
				Description("Não, para APIs que só servem rotas.").Affirmative("Sim").Negative("Não").Value(&o.ViteAssets),
		)
	case project.Symfony:
		fields = append(fields,
			huh.NewInput().Title("Versão do PHP").Value(&o.PHPVersion),
			huh.NewConfirm().Title("Compilar o AssetMapper (asset-map:compile)?").Affirmative("Sim").Negative("Não").Value(&o.AssetMapper),
			huh.NewConfirm().Title("Buildar assets com Node (Encore/Vite: npm run build)?").Affirmative("Sim").Negative("Não").Value(&o.ViteAssets),
		)
	case project.React, project.Angular:
		if o.Type == project.Angular {
			fields = append(fields, huh.NewInput().Title("Pasta publicada (saída do ng build)").Value(&o.OutputDir))
		}
		fields = append(fields,
			huh.NewSelect[string]().Title("Gerenciador de pacotes").
				Options(huh.NewOption("pnpm", "pnpm"), huh.NewOption("npm", "npm")).Value(&o.PackageManager),
			huh.NewInput().Title("Versão do Node").Value(&o.NodeVersion),
			huh.NewConfirm().Title("Rodar lint antes do build?").Affirmative("Sim").Negative("Não").Value(&o.Lint),
		)
	case project.Static:
		if !o.MkDocs {
			fields = append(fields,
				huh.NewInput().Title("Comando de build (vazio = conteúdo já pronto)").Value(&o.BuildCommand),
				huh.NewInput().Title("Pasta publicada").Value(&o.OutputDir),
			)
		}
	}
	vars := strings.Join(o.ViteVars, ", ")
	if o.Type == project.React || ((o.Type == project.Laravel || o.Type == project.Symfony) && o.ViteAssets) {
		fields = append(fields, huh.NewInput().Title("Variáveis do build (vars do repositório)").
			Description("Separadas por vírgula. Detectadas no .env.example.").Value(&vars))
	}
	if len(fields) > 0 {
		if err := env.UI.Run(huh.NewGroup(fields...)); err != nil {
			return err
		}
	}
	o.ViteVars = nil
	for _, v := range strings.Split(vars, ",") {
		if v = strings.TrimSpace(v); v != "" {
			o.ViteVars = append(o.ViteVars, v)
		}
	}
	return nil
}

func nextSteps(dir string, o project.Options) []string {
	steps := []string{
		"O app precisa existir no servidor: `sudo deploy-tools app create " + o.App + " --type " + o.Type + " --domain <domínio>`.",
		"Crie os secrets DEPLOY_HOST, DEPLOY_USER, DEPLOY_SSH_KEY e DEPLOY_HOST_KEY (Settings → Secrets and variables → Actions; " +
			"na organização, se vários repositórios publicam no mesmo servidor).",
	}
	if len(o.ViteVars) > 0 {
		steps = append(steps, "Crie as variáveis do repositório (Settings → Secrets and variables → Actions → Variables): "+strings.Join(o.ViteVars, ", ")+".")
	}
	if o.Commitizen {
		steps = append(steps, "Se a branch "+o.Branch+" for protegida, libere o push do github-actions[bot] (commit \"bump:\" e tag) "+
			"ou troque o GITHUB_TOKEN por um PAT.")
	}
	if o.Commitizen {
		if _, err := os.Stat(filepath.Join(dir, "cz.toml")); err != nil {
			if _, err := os.Stat(filepath.Join(dir, ".cz.toml")); err != nil {
				steps = append(steps, "Configure o Commitizen no projeto: `cz init` (gera o cz.toml).")
			}
		}
	}
	steps = append(steps, "Valide com `deploy-dev check` e faça o commit (ex.: `ci: adiciona deploy`).")
	return steps
}

func checkCmd(env *Env) *cobra.Command {
	var strict bool
	cmd := &cobra.Command{
		Use:   "check [dir]",
		Short: "Valida os workflows do projeto contra as convenções de deploy",
		Long: `Verifica .github/workflows/*.yml: actions fixadas por SHA (com a versão
comentada), formato do release id, passo de SSH copiado que pode virar a
action do deploy-tools, restos de Gitea (contexto gitea.*, URL em uses:,
checkout local da action), proteção contra loop do bump e versão da action
de deploy.

Sai com código 1 se houver erros (ou avisos, com --strict).`,
		Example: "  deploy-dev check\n  deploy-dev check ../outro-projeto --strict",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			dir := "."
			if len(args) > 0 {
				dir = args[0]
			}
			return runCheck(env, dir, strict)
		},
	}
	cmd.Flags().BoolVar(&strict, "strict", false, "avisos também falham (código 1)")
	return cmd
}

func runCheck(env *Env, dir string, strict bool) error {
	fs, err := project.Check(dir, env.Commit, env.Version)
	if err != nil {
		return err
	}
	counts := map[project.Level]int{}
	for _, f := range fs {
		counts[f.Level]++
		switch f.Level {
		case project.Error:
			env.UI.Println(ui.ErrorText(f.File + ":" + fmt.Sprint(f.Line) + ": " + f.Msg))
		case project.Warn:
			env.UI.Warn("%s:%d: %s", f.File, f.Line, f.Msg)
		default:
			env.UI.Println(env.UI.Faint(fmt.Sprintf("  %s:%d: %s", f.File, f.Line, f.Msg)))
		}
	}
	if counts[project.Error] == 0 && counts[project.Warn] == 0 {
		env.UI.OK("Workflows ok.")
		return nil
	}
	env.UI.Println()
	env.UI.Println(fmt.Sprintf("%d erro(s), %d aviso(s).", counts[project.Error], counts[project.Warn]))
	if counts[project.Error] > 0 || (strict && counts[project.Warn] > 0) {
		return errFindings
	}
	return nil
}
