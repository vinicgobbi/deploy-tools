package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/vinicgobbi/deploy-tools/internal/config"
	"github.com/vinicgobbi/deploy-tools/internal/provision"
)

// installState is cached: InstallState runs the installed binary to read
// its version, and the TUI asks on every redraw.
func (env *Env) installState() provision.InstallState {
	if env.install == nil {
		st := env.P.InstallState(env.Self, env.Version)
		env.install = &st
	}
	return *env.install
}

// compareVersions compares "vX.Y.Z[-anything]"; ok=false when either is
// not a version (e.g. "dev").
func compareVersions(a, b string) (cmp int, ok bool) {
	pa, oka := parseVersion(a)
	pb, okb := parseVersion(b)
	if !oka || !okb {
		return 0, false
	}
	for i := range pa {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1, true
			}
			return 1, true
		}
	}
	return 0, true
}

func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// installSummary describes whether this binary is the installed one or a
// loose copy, with what to do about it. loose is true for a loose copy.
func installSummary(env *Env) (lines []string, loose bool) {
	st := env.installState()
	bin := env.Paths.BinaryPath()
	switch {
	case st.Running:
		lines = append(lines, fmt.Sprintf("Instalado em %s (%s).", bin, env.Version))
	case !st.Installed:
		lines = append(lines,
			fmt.Sprintf("Este binário (%s) está rodando solto de %s.", env.Version, st.Self),
			"O deploy-tools ainda não está instalado neste servidor.",
			"Para preparar o servidor: sudo "+st.Self+" server setup --key ci_deploy_key.pub")
		return lines, true
	default:
		installed := st.InstalledVersion
		if installed == "" {
			installed = "versão desconhecida"
		}
		lines = append(lines,
			fmt.Sprintf("Este binário (%s) está rodando solto de %s.", env.Version, st.Self),
			fmt.Sprintf("Instalado: %s em %s.", installed, bin))
		switch c, ok := compareVersions(env.Version, st.InstalledVersion); {
		case ok && c > 0:
			lines = append(lines, "Este é mais novo. Para atualizar: sudo "+st.Self+" server install")
		case ok && c < 0:
			lines = append(lines, "Este é mais antigo que o instalado: use "+bin+".")
		case ok:
			lines = append(lines, "É a mesma versão: pode usar "+bin+" direto.")
		default:
			lines = append(lines, "Para instalar este: sudo "+st.Self+" server install")
		}
		loose = true
	}
	if st.Installed && !st.Wrapper {
		lines = append(lines, env.Paths.WrapperPath()+" não aponta para o binário instalado: rode sudo deploy-tools server install.")
	}
	return lines, loose
}

func printInstallSummary(env *Env) {
	lines, loose := installSummary(env)
	for i, l := range lines {
		switch {
		case !loose:
			env.UI.Println(env.UI.Faint(l))
		case i == 0:
			env.UI.Warn("%s", l)
		default:
			env.UI.Hint("%s", l)
		}
	}
}

// secretsTarget is where `gh secret set` writes: a repository or an org.
type secretsTarget struct{ repo, org string }

func (t secretsTarget) flag() string {
	switch {
	case t.org != "":
		return "--org " + t.org + " --visibility all"
	case t.repo != "":
		return "-R " + t.repo
	}
	return "-R <dono>/<repo>"
}

const secretsWarning = "Não compartilhe estes dados (chat, ticket, e-mail, print de tela). Com eles e a chave privada, " +
	"qualquer um publica em todos os apps deste servidor. Cole-os só nos secrets do GitHub."

// printSecrets shows the GitHub secrets for this server. privateKey is the
// local file name of the CI private key, as the operator knows it.
func printSecrets(env *Env, s provision.Secrets, privateKey string, target secretsTarget) {
	if privateKey == "" {
		privateKey = "ci_deploy_key"
	}
	env.UI.Title("Secrets do GitHub")
	env.UI.Println(env.UI.Faint("Settings → Secrets and variables → Actions (no repositório, ou na organização para vários repositórios)"))
	env.UI.Println()
	env.UI.Warn("%s", secretsWarning)
	env.UI.Println()

	hostKeys := strings.Join(s.HostKeys, "\n")
	if hostKeys == "" {
		hostKeys = "(nenhuma host key em " + env.Paths.SSHDir + "; rode no servidor: ssh-keyscan " + s.Host + ")"
	}
	env.UI.KV(
		[2]string{"DEPLOY_HOST", s.Host},
		[2]string{"DEPLOY_USER", s.User},
		[2]string{"DEPLOY_SSH_KEY", "conteúdo inteiro do arquivo " + privateKey + " (a chave PRIVADA, par do .pub autorizado)"},
		[2]string{"DEPLOY_HOST_KEY", ""},
	)
	for _, l := range strings.Split(hostKeys, "\n") {
		env.UI.Println("    " + l)
	}
	env.UI.Println()
	env.UI.Hint("%s", "DEPLOY_HOST é o endereço pelo qual o runner do GitHub alcança este servidor (DNS ou IP público). "+
		"Se não for "+s.Host+", rode de novo com --host ou grave DEPLOY_HOST em "+config.File+".")
	env.UI.Hint("%s", "O servidor não guarda a chave privada. Se ela se perdeu, gere outra: sudo deploy-tools key generate.")
	if s.CIKeys != nil {
		if len(s.CIKeys) == 0 {
			env.UI.Hint("Nenhuma chave da CI autorizada ainda: sudo deploy-tools key add %s.pub", privateKey)
		} else {
			env.UI.Println()
			env.UI.Println(env.UI.Faint("Chaves da CI autorizadas (a DEPLOY_SSH_KEY precisa ser o par de uma delas):"))
			for _, k := range s.CIKeys {
				env.UI.Println("    " + k.Fingerprint() + "  " + orDash(k.Comment))
			}
			env.UI.Println(env.UI.Faint("    confira na sua máquina: ssh-keygen -lf " + privateKey))
		}
	}

	env.UI.Println()
	env.UI.Println(env.UI.Faint("Ou pelo gh CLI, na máquina onde está a chave privada (nada passa pela tela):"))
	f := target.flag()
	env.UI.Println("    gh secret set DEPLOY_HOST " + f + " --body '" + s.Host + "'")
	env.UI.Println("    gh secret set DEPLOY_USER " + f + " --body '" + s.User + "'")
	env.UI.Println("    gh secret set DEPLOY_SSH_KEY " + f + " < " + privateKey)
	if len(s.HostKeys) > 0 {
		env.UI.Printf("    printf '%%s\\n' \\\n")
		for _, l := range s.HostKeys {
			env.UI.Println("      '" + l + "' \\")
		}
		env.UI.Println("      | gh secret set DEPLOY_HOST_KEY " + f)
	}
}

// privateKeyName guesses the private key's name from the .pub path.
func privateKeyName(pub string) string {
	name := pub
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	return strings.TrimSuffix(name, ".pub")
}

func secretsCmd(env *Env) *cobra.Command {
	var host string
	var t secretsTarget
	cmd := &cobra.Command{
		Use:   "secrets",
		Short: "Mostra como devem ficar os secrets do GitHub para este servidor",
		Long: `Mostra os valores de DEPLOY_HOST, DEPLOY_USER e DEPLOY_HOST_KEY (lidos
deste servidor), de onde vem a DEPLOY_SSH_KEY e os comandos "gh secret set"
equivalentes. A chave privada não fica no servidor e nunca é mostrada.

Não compartilhe a saída: cole os valores só nos secrets do GitHub.`,
		Example: `  deploy-tools key secrets
  deploy-tools key secrets --host deploy.example.com --repo acme/site
  deploy-tools key secrets --org acme`,
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			printSecrets(env, env.P.Secrets(host), "", t)
			return nil
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "endereço do servidor para a CI (padrão: DEPLOY_HOST de "+config.File+" ou o hostname)")
	cmd.Flags().StringVar(&t.repo, "repo", "", "repositório dono/nome, para os comandos gh")
	cmd.Flags().StringVar(&t.org, "org", "", "organização, para os comandos gh (secrets compartilhados)")
	cmd.MarkFlagsMutuallyExclusive("repo", "org")
	return cmd
}

func statusCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Mostra se o deploy-tools está instalado ou rodando solto",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			env.UI.Println("deploy-tools", env.Version)
			printInstallSummary(env)
			return nil
		},
	}
}

const privateKeyWarning = "Esta é a chave PRIVADA da CI: ela publica em todos os apps deste servidor. " +
	"Copie agora para o secret DEPLOY_SSH_KEY do GitHub. Ela não fica salva no servidor e não será mostrada de novo. " +
	"Não compartilhe (chat, ticket, e-mail, print de tela)."

// doGenerateKey creates and authorizes a CI key pair on the server. The
// private key is printed once or, with out, written to that file (0600).
func doGenerateKey(env *Env, comment, out string) error {
	if comment == "" {
		host, _ := os.Hostname()
		comment = "ci-deploy@" + host
	}
	if out != "" {
		if _, err := os.Lstat(out); err == nil {
			return fmt.Errorf("%s já existe — escolha outro caminho", out)
		}
	}
	priv, key, err := env.P.GenerateKey(comment)
	if err != nil {
		return err
	}
	name := "ci_deploy_key"
	if out != "" {
		if err := os.WriteFile(out, priv, 0o600); err != nil {
			return fmt.Errorf("a chave pública já foi autorizada, mas a privada não pôde ser gravada (%w) — "+
				"remova a linha %s do %s e gere de novo", err, key.Fingerprint(), env.Paths.AuthorizedKeys())
		}
		env.UI.OK("Chave privada gravada em %s (só root lê). Depois de cadastrar no GitHub, apague o arquivo.", out)
		name = out
	} else {
		env.UI.Println()
		env.UI.Warn("%s", privateKeyWarning)
		env.UI.Println()
		env.UI.Println(strings.TrimRight(string(priv), "\n"))
		env.UI.Println()
		env.UI.Hint("Na sua máquina, salve como ci_deploy_key (chmod 600) para usar com o gh, ou cole direto no GitHub.")
	}
	env.UI.Println()
	printSecrets(env, env.P.Secrets(""), name, secretsTarget{})
	return nil
}

func generateKeyCmd(env *Env) *cobra.Command {
	var comment, out string
	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Gera a chave da CI aqui no servidor e já a autoriza",
		Long: `Gera um par ed25519 em memória, autoriza a chave pública (restrita ao
deploy-ci) e mostra a chave PRIVADA uma única vez, para o secret
DEPLOY_SSH_KEY do GitHub. Ela não fica salva no servidor; com --out, vai
para esse arquivo (modo 600) em vez da tela.`,
		Example: `  sudo deploy-tools key generate
  sudo deploy-tools key generate --out /root/ci_deploy_key   # e depois: gh secret set DEPLOY_SSH_KEY < /root/ci_deploy_key`,
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if err := env.RequireRoot(); err != nil {
				return err
			}
			return doGenerateKey(env, comment, out)
		},
	}
	cmd.Flags().StringVar(&comment, "comment", "", "comentário da chave (padrão: ci-deploy@<hostname>)")
	cmd.Flags().StringVar(&out, "out", "", "grava a chave privada neste arquivo em vez de mostrar")
	return cmd
}

// askKeySource is the TUI choice for the CI key: generate, file or skip.
func askKeySource(env *Env, allowSkip bool) (choice, path string, err error) {
	choice = "generate"
	opts := []huh.Option[string]{
		huh.NewOption("Gerar agora, aqui no servidor (a chave privada aparece uma vez)", "generate"),
		huh.NewOption("Usar um .pub que já gerei na minha máquina", "file"),
	}
	if allowSkip {
		opts = append(opts, huh.NewOption("Pular por enquanto", "skip"))
	}
	if err := env.UI.Select("Chave da CI", &choice, opts...); err != nil {
		return "", "", err
	}
	if choice == "file" {
		if err := env.UI.Input("Caminho da chave PÚBLICA (.pub)", "ci_deploy_key.pub", &path, fileExists); err != nil {
			return "", "", err
		}
	}
	return choice, path, nil
}
