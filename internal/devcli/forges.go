package devcli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/vinicgobbi/deploy-tools/internal/project"
)

// The registered instances (GitHub, Gitea or Forgejo on a custom domain)
// live in <config dir>/deploy-dev/forges.yml; github.com is built in.

type forgeFile struct {
	Forges []project.Forge `yaml:"forges"`
}

func (env *Env) forgesPath() string {
	if env.ForgesFile != "" {
		return env.ForgesFile
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "deploy-dev", "forges.yml")
}

// registered is what forges.yml holds (without the built-in github.com).
func (env *Env) registered() ([]project.Forge, error) {
	path := env.forgesPath()
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ff forgeFile
	if err := yaml.Unmarshal(b, &ff); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return ff.Forges, nil
}

// forges is github.com followed by the registered instances.
func (env *Env) forges() ([]project.Forge, error) {
	reg, err := env.registered()
	return append([]project.Forge{project.GitHubCom}, reg...), err
}

func (env *Env) saveForges(list []project.Forge) error {
	path := env.forgesPath()
	if path == "" {
		return errors.New("não encontrei a pasta de configuração do usuário")
	}
	b, err := yaml.Marshal(forgeFile{Forges: list})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	header := "# Instâncias cadastradas no deploy-dev (deploy-dev forge add/remove).\n"
	return os.WriteFile(path, append([]byte(header), b...), 0o644)
}

func findForge(list []project.Forge, name string) (project.Forge, bool) {
	for _, f := range list {
		if f.Name == name {
			return f, true
		}
	}
	return project.Forge{}, false
}

// originRemote is the URL of the project's "origin" remote, if any.
func originRemote(dir string) string {
	out, err := exec.Command("git", "-C", dir, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// resolveForge picks the forge for the project in dir: --forge, else the
// one matching the origin remote's host, else github.com when there is no
// remote. An unknown host is asked about (interactive, with instances to
// pick from) or is an error: generating for the wrong forge would put the
// workflow where nothing runs it.
func resolveForge(env *Env, dir, name string, interactive bool) (project.Forge, error) {
	list, err := env.forges()
	if err != nil {
		return project.Forge{}, err
	}
	if name != "" {
		if f, ok := findForge(list, name); ok {
			return f, nil
		}
		return project.Forge{}, fmt.Errorf("instância %q não cadastrada — veja `deploy-dev forge list`", name)
	}
	remote := originRemote(dir)
	if f, ok := project.MatchForge(list, remote); ok {
		return f, nil
	}
	if remote == "" {
		return list[0], nil
	}
	host := project.RemoteHost(remote)
	if interactive && len(list) > 1 {
		f := list[0]
		if err := selectForge(env, list, &f, "O remote origin aponta para "+host+", que não está cadastrado. Gerar para qual instância?"); err != nil {
			return project.Forge{}, err
		}
		return f, nil
	}
	return project.Forge{}, fmt.Errorf("o remote origin aponta para %s, que não está cadastrado — cadastre a instância "+
		"(`deploy-dev forge add <nome> --kind gitea --url https://%s`) ou escolha uma com --forge", host, host)
}

func selectForge(env *Env, list []project.Forge, f *project.Forge, title string) error {
	name := f.Name
	if name == "" {
		name = project.GitHubCom.Name
	}
	opts := make([]huh.Option[string], len(list))
	for i, x := range list {
		opts[i] = huh.NewOption(x.Label(), x.Name)
	}
	if err := env.UI.Select(title, &name, opts...); err != nil {
		return err
	}
	*f, _ = findForge(list, name)
	return nil
}

func forgeCmd(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "forge",
		Short: "Instâncias de GitHub, Gitea ou Forgejo para onde o init gera o workflow",
		Long: `Cadastra as instâncias (com domínio próprio) onde ficam os repositórios.
O init escolhe a instância pelo host do remote origin do projeto, ou pela
opção --forge. O github.com já vem cadastrado.

O cadastro fica em ` + "`" + env.forgesPath() + "`" + `.`,
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error { return listForges(env) },
	}
	cmd.AddCommand(forgeAddCmd(env), forgeListCmd(env), forgeRemoveCmd(env))
	return cmd
}

func forgeListCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Lista as instâncias cadastradas",
		Args:  cobra.NoArgs,
		RunE:  func(*cobra.Command, []string) error { return listForges(env) },
	}
}

func listForges(env *Env) error {
	list, err := env.forges()
	if err != nil {
		return err
	}
	env.UI.Title("Instâncias")
	for i, f := range list {
		pairs := [][2]string{{"tipo", project.KindName(f.Kind)}, {"url", f.URL}}
		if f.Kind != project.GitHub {
			actions := f.ActionsURL
			if actions == "" {
				actions = "padrão da instância"
				if f.Kind == project.Forgejo {
					actions = "https://github.com (o padrão do Forgejo não tem todas as actions usadas)"
				}
			}
			pairs = append(pairs, [2]string{"actions de", actions}, [2]string{"runner", f.RunsOn()})
		}
		name := f.Name
		if i == 0 {
			name += env.UI.Faint("  (embutida)")
		}
		env.UI.Println(name)
		env.UI.KV(pairs...)
	}
	return nil
}

func forgeAddCmd(env *Env) *cobra.Command {
	var f project.Forge
	var force bool
	cmd := &cobra.Command{
		Use:   "add <nome>",
		Short: "Cadastra uma instância (Gitea, Forgejo ou GitHub com domínio próprio)",
		Long: `Cadastra uma instância. O nome é como você a escolhe no init (--forge).

--actions-url é de onde vêm as actions do workflow (checkout, setup-node...):
deixe vazio para usar o padrão da instância (no Gitea, o github.com) ou
informe um mirror, se a instância não alcança o github.com. As actions são
referenciadas como <actions-url>/dono/repo@sha, então o mirror precisa
manter o mesmo dono/repo e os commits originais.`,
		Example: `  deploy-dev forge add empresa --kind gitea --url https://git.empresa.com
  deploy-dev forge add casa --kind forgejo --url https://code.casa.dev --runner docker
  deploy-dev forge add interno --kind gitea --url https://git.acme.com --actions-url https://git.acme.com/mirror`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			f.Name = args[0]
			if env.UI.Interactive && (f.Kind == "" || f.URL == "") {
				if err := askForge(env, &f); err != nil {
					return err
				}
			}
			return addForge(env, f, force)
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&f.Kind, "kind", "", strings.Join(project.Kinds, " | "))
	fl.StringVar(&f.URL, "url", "", "endereço web da instância (ex.: https://git.empresa.com)")
	fl.StringVar(&f.ActionsURL, "actions-url", "", "de onde vêm as actions (padrão: o da instância)")
	fl.StringVar(&f.Runner, "runner", "", "label do runs-on (padrão: ubuntu-latest)")
	fl.BoolVar(&force, "force", false, "substitui uma instância com o mesmo nome")
	return cmd
}

// addForge registers f, replacing one with the same name only with force.
func addForge(env *Env, f project.Forge, force bool) error {
	f.URL = strings.TrimSuffix(f.URL, "/")
	f.ActionsURL = strings.TrimSuffix(f.ActionsURL, "/")
	if err := f.Validate(); err != nil {
		return err
	}
	reg, err := env.registered()
	if err != nil {
		return err
	}
	if f.Name == project.GitHubCom.Name {
		return errors.New(`"github" é a instância embutida (github.com): escolha outro nome`)
	}
	_, exists := findForge(reg, f.Name)
	if exists && !force {
		return fmt.Errorf("a instância %q já existe — use --force para substituir", f.Name)
	}
	out := []project.Forge{}
	for _, x := range reg {
		if x.Name == f.Name {
			x = f // keeps its position when replaced
		}
		out = append(out, x)
	}
	if !exists {
		out = append(out, f)
	}
	if err := env.saveForges(out); err != nil {
		return err
	}
	env.UI.OK("Instância %s cadastrada.", f.Label())
	env.UI.Hint("O init a escolhe sozinho quando o remote origin aponta para %s; ou use `deploy-dev init --forge %s`.", f.Host(), f.Name)
	return nil
}

// removeForge drops the registered instance name.
func removeForge(env *Env, name string) error {
	reg, err := env.registered()
	if err != nil {
		return err
	}
	var out []project.Forge
	for _, x := range reg {
		if x.Name != name {
			out = append(out, x)
		}
	}
	if len(out) == len(reg) {
		return fmt.Errorf("instância %q não cadastrada", name)
	}
	if err := env.saveForges(out); err != nil {
		return err
	}
	env.UI.OK("Instância %s removida.", name)
	return nil
}

func askForge(env *Env, f *project.Forge) error {
	if f.Kind == "" {
		f.Kind = project.Gitea
	}
	kinds := make([]huh.Option[string], len(project.Kinds))
	for i, k := range project.Kinds {
		kinds[i] = huh.NewOption(project.KindName(k), k)
	}
	return env.UI.Run(huh.NewGroup(
		huh.NewSelect[string]().Title("Tipo").Options(kinds...).Value(&f.Kind),
		huh.NewInput().Title("Endereço web da instância").Placeholder("https://git.empresa.com").Value(&f.URL),
		huh.NewInput().Title("De onde vêm as actions").
			Description("Vazio = padrão da instância. Um mirror, se ela não alcança o github.com.").Value(&f.ActionsURL),
		huh.NewInput().Title("Label do runner (runs-on)").Description("Vazio = ubuntu-latest.").Value(&f.Runner),
	))
}

func forgeRemoveCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:     "remove <nome>",
		Aliases: []string{"rm"},
		Short:   "Remove uma instância cadastrada",
		Args:    cobra.ExactArgs(1),
		RunE:    func(_ *cobra.Command, args []string) error { return removeForge(env, args[0]) },
	}
}
