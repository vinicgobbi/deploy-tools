package cli

import (
	"fmt"
	"os"
	"path/filepath"
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
	for _, l := range strings.Split(ghCommands(s, privateKey, target), "\n") {
		env.UI.Println("    " + l)
	}
}

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// ghScript is a standalone script that sets the secrets on the repository
// (or organization) given as its first argument; the private key file is
// keyPath unless a second argument overrides it.
func ghScript(s provision.Secrets, keyPath string) string {
	if keyPath == "" {
		keyPath = "ci_deploy_key"
	}
	var b strings.Builder
	b.WriteString(`#!/bin/sh
# Secrets do GitHub para publicar com deploy-tools em ` + s.Host + `.
# Uso: sh github-secrets.sh <dono/repo> [arquivo-da-chave-privada]
#      sh github-secrets.sh --org <organização> [arquivo-da-chave-privada]
# Precisa do gh logado (gh auth login). Não compartilhe este arquivo; apague depois de usar.
set -eu
usage() { echo "uso: $0 <dono/repo> | --org <organização>  [arquivo-da-chave-privada]" >&2; exit 2; }
if [ "${1:-}" = "--org" ]; then
  [ -n "${2:-}" ] || usage
  ORG=$2 KEY=${3:-` + shQuote(keyPath) + `}
  secret() { gh secret set "$1" --org "$ORG" --visibility all; }
else
  [ -n "${1:-}" ] || usage
  REPO=$1 KEY=${2:-` + shQuote(keyPath) + `}
  secret() { gh secret set "$1" -R "$REPO"; }
fi
[ -r "$KEY" ] || { echo "chave privada não encontrada: $KEY (passe o caminho como último argumento)" >&2; exit 1; }

printf '%s' ` + shQuote(s.Host) + ` | secret DEPLOY_HOST
printf '%s' ` + shQuote(s.User) + ` | secret DEPLOY_USER
secret DEPLOY_SSH_KEY < "$KEY"
`)
	if len(s.HostKeys) > 0 {
		b.WriteString("printf '%s\\n' \\\n")
		for _, l := range s.HostKeys {
			b.WriteString("  " + shQuote(l) + " \\\n")
		}
		b.WriteString("  | secret DEPLOY_HOST_KEY\n")
	}
	b.WriteString(`echo "Secrets DEPLOY_HOST, DEPLOY_USER, DEPLOY_SSH_KEY e DEPLOY_HOST_KEY gravados."` + "\n")
	return b.String()
}

// freeScriptPath is dir/github-secrets.sh, or -2, -3... if taken.
func freeScriptPath(dir string) string {
	for i := 1; ; i++ {
		name := "github-secrets.sh"
		if i > 1 {
			name = "github-secrets-" + strconv.Itoa(i) + ".sh"
		}
		if p := filepath.Join(dir, name); func() bool { _, err := os.Lstat(p); return os.IsNotExist(err) }() {
			return p
		}
	}
}

// writeScript saves ghScript to path (0600, owned by the sudo user).
func writeScript(path, script string) error {
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("%s já existe — escolha outro caminho", path)
	}
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		return err
	}
	chownToInvoker(path)
	return nil
}

// askDeployHost asks how the CI reaches this server: the current value,
// the hostname, an interface IP or a typed address (e.g. the public IP
// behind NAT).
func askDeployHost(env *Env) (string, error) {
	cur := env.Paths.DeployHost
	var opts []huh.Option[string]
	if cur != "" {
		opts = append(opts, huh.NewOption(cur+"   (atual)", cur))
	}
	for _, c := range provision.HostCandidates() {
		if c.Value != cur {
			opts = append(opts, huh.NewOption(c.Value+"   ("+c.Label+")", c.Value))
		}
	}
	opts = append(opts, huh.NewOption("Outro: digitar (IP público, DNS...)", otherDomain))
	choice := opts[0].Value
	if err := env.UI.Select("Como o GitHub vai acessar este servidor? Vira o DEPLOY_HOST e entra no DEPLOY_HOST_KEY "+
		"(o SSH confere o host key pelo mesmo nome ou IP usado na conexão).", &choice, opts...); err != nil {
		return "", err
	}
	if choice == otherDomain {
		choice = ""
		if err := env.UI.Input("Endereço do servidor (hostname ou IP)", "deploy.example.com", &choice, provision.ValidateHost); err != nil {
			return "", err
		}
	}
	return choice, nil
}

// ensureDeployHost asks for DEPLOY_HOST on a terminal (always, or only
// when unset) and saves it when running as root.
func ensureDeployHost(env *Env, always bool) error {
	if !env.UI.Interactive || (!always && env.Paths.DeployHost != "") {
		return nil
	}
	h, err := askDeployHost(env)
	if err != nil || h == env.Paths.DeployHost {
		return err
	}
	if env.RequireRoot() != nil {
		env.P.Paths.DeployHost, env.Paths.DeployHost = h, h
		return nil
	}
	if err := env.P.SetDeployHost(h); err != nil {
		return err
	}
	syncPaths(env)
	return nil
}

// ghCommands are the `gh secret set` commands for s, one per line (the
// host keys one spans several).
func ghCommands(s provision.Secrets, privateKey string, target secretsTarget) string {
	if privateKey == "" {
		privateKey = "ci_deploy_key"
	}
	f := target.flag()
	lines := []string{
		"gh secret set DEPLOY_HOST " + f + " --body '" + s.Host + "'",
		"gh secret set DEPLOY_USER " + f + " --body '" + s.User + "'",
		"gh secret set DEPLOY_SSH_KEY " + f + " < " + privateKey,
	}
	if len(s.HostKeys) > 0 {
		lines = append(lines, "printf '%s\\n' \\")
		for _, l := range s.HostKeys {
			lines = append(lines, "  '"+l+"' \\")
		}
		lines = append(lines, "  | gh secret set DEPLOY_HOST_KEY "+f)
	}
	return strings.Join(lines, "\n")
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
	var copyGh, ghOnly, script bool
	cmd := &cobra.Command{
		Use:   "secrets",
		Short: "Mostra como devem ficar os secrets do GitHub para este servidor",
		Long: `Mostra os valores de DEPLOY_HOST, DEPLOY_USER e DEPLOY_HOST_KEY (lidos
deste servidor), de onde vem a DEPLOY_SSH_KEY e os comandos "gh secret set"
equivalentes. A chave privada não fica no servidor e nunca é mostrada.

Não compartilhe a saída: cole os valores só nos secrets do GitHub.`,
		Example: `  deploy-tools key secrets
  deploy-tools key secrets --host deploy.example.com --repo acme/site
  deploy-tools key secrets --org acme
  deploy-tools key secrets --script > github-secrets.sh && sh github-secrets.sh acme/site ~/ci_deploy_key`,
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			s := env.P.Secrets(host)
			switch {
			case script:
				env.UI.Printf("%s", ghScript(s, ""))
				return nil
			case ghOnly:
				env.UI.Println(ghCommands(s, "", t))
			default:
				printSecrets(env, s, "", t)
			}
			if copyGh {
				if err := copyToClipboard(env, ghCommands(s, "", t)); err != nil {
					return err
				}
				env.UI.OK("Comandos gh enviados para a área de transferência do seu terminal (OSC 52).")
			}
			return offerCopy(env, ghCommands(s, "", t), ghScript(s, ""))
		},
	}
	cmd.Flags().BoolVar(&script, "script", false, "imprime um script que recebe o repositório: sh github-secrets.sh dono/repo [chave]")
	cmd.Flags().BoolVar(&copyGh, "copy", false, "copia os comandos gh para a área de transferência do seu terminal (OSC 52, funciona por SSH)")
	cmd.Flags().BoolVar(&ghOnly, "gh-only", false, "imprime só os comandos gh, sem formatação (para > arquivo.sh)")
	cmd.Flags().StringVar(&host, "host", "", "endereço do servidor para a CI (padrão: DEPLOY_HOST de "+config.File+" ou o hostname)")
	cmd.Flags().StringVar(&t.repo, "repo", "", "repositório dono/nome, para os comandos gh")
	cmd.Flags().StringVar(&t.org, "org", "", "organização, para os comandos gh (secrets compartilhados)")
	cmd.MarkFlagsMutuallyExclusive("script", "gh-only")
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
	"Cadastre no secret DEPLOY_SSH_KEY do GitHub e depois apague o arquivo. " +
	"Não compartilhe (chat, ticket, e-mail, print de tela)."

// keyGen is how doGenerateKey hands over the private key.
type keyGen struct {
	comment   string
	out       string // file for the private key; "" = ./ci_deploy_key (next free name)
	printOnly bool   // print it once instead of saving
}

// freeKeyPath is dir/ci_deploy_key, or ci_deploy_key-2, -3... so nothing
// is ever overwritten (the .pub next to it included).
func freeKeyPath(dir string) string {
	for i := 1; ; i++ {
		name := "ci_deploy_key"
		if i > 1 {
			name += "-" + strconv.Itoa(i)
		}
		p := filepath.Join(dir, name)
		_, errPriv := os.Lstat(p)
		_, errPub := os.Lstat(p + ".pub")
		if os.IsNotExist(errPriv) && os.IsNotExist(errPub) {
			return p
		}
	}
}

// chownToInvoker gives a file created under sudo to the user who ran sudo,
// so their own gh (or scp) can read it.
func chownToInvoker(path string) {
	uid, err1 := strconv.Atoi(os.Getenv("SUDO_UID"))
	gid, err2 := strconv.Atoi(os.Getenv("SUDO_GID"))
	if err1 == nil && err2 == nil && uid != 0 {
		_ = os.Lchown(path, uid, gid)
	}
}

// doGenerateKey creates and authorizes a CI key pair on the server. The
// private key is saved (0600, owned by the sudo user) in the directory
// deploy-tools was run from, or where g.out says; g.printOnly shows it
// once instead.
func doGenerateKey(env *Env, g keyGen) error {
	if err := ensureDeployHost(env, false); err != nil {
		return err
	}
	if g.comment == "" {
		host, _ := os.Hostname()
		g.comment = "ci-deploy@" + host
	}
	out := g.out
	if out != "" {
		if _, err := os.Lstat(out); err == nil {
			return fmt.Errorf("%s já existe — escolha outro caminho", out)
		}
	} else if !g.printOnly {
		dir, err := os.Getwd()
		if err != nil {
			return err
		}
		out = freeKeyPath(dir)
	}
	if out != "" {
		if abs, err := filepath.Abs(out); err == nil {
			out = abs
		}
	}
	priv, key, err := env.P.GenerateKey(g.comment)
	if err != nil {
		return err
	}
	name := "ci_deploy_key"
	if out != "" {
		pub := key.Type + " " + key.Blob + " " + key.Comment + "\n"
		if err := os.WriteFile(out, priv, 0o600); err != nil {
			return fmt.Errorf("a chave pública já foi autorizada, mas a privada não pôde ser gravada (%w) — "+
				"remova a linha %s do %s e gere de novo", err, key.Fingerprint(), env.Paths.AuthorizedKeys())
		}
		_ = os.WriteFile(out+".pub", []byte(pub), 0o644)
		chownToInvoker(out)
		chownToInvoker(out + ".pub")
		env.UI.OK("Chave da CI gerada e autorizada: %s", key.Fingerprint())
		env.UI.KV(
			[2]string{"chave privada", out + "  " + env.UI.Faint("(modo 600)")},
			[2]string{"chave pública", out + ".pub"},
		)
		env.UI.Println()
		env.UI.Warn("%s", privateKeyWarning)
		name = out
	} else {
		env.UI.Println()
		env.UI.Warn("%s", privateKeyWarning+" Ela não fica salva no servidor e não será mostrada de novo.")
		env.UI.Println()
		env.UI.Println(strings.TrimRight(string(priv), "\n"))
		env.UI.Println()
		env.UI.Hint("Na sua máquina, salve como ci_deploy_key (chmod 600) para usar com o gh, ou cole direto no GitHub.")
	}
	env.UI.Println()
	s := env.P.Secrets("")
	printSecrets(env, s, name, secretsTarget{})
	if out != "" {
		script := freeScriptPath(filepath.Dir(out))
		if err := writeScript(script, ghScript(s, out)); err == nil {
			env.UI.Println()
			env.UI.OK("Script dos secrets gravado em %s", script)
			env.UI.Hint("Para cadastrar num repositório (com o gh logado): sh %s dono/repo  —  ou --org <organização>", script)
		}
	}
	return offerCopy(env, ghCommands(s, name, secretsTarget{}), ghScript(s, name),
		copyItem{"a chave privada (DEPLOY_SSH_KEY)", string(priv)})
}

func generateKeyCmd(env *Env) *cobra.Command {
	var g keyGen
	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Gera a chave da CI aqui no servidor e já a autoriza",
		Long: `Gera um par ed25519, autoriza a chave pública (restrita ao deploy-ci) e
grava o par na pasta de onde o comando foi chamado: ci_deploy_key (modo
600) e ci_deploy_key.pub, ou ci_deploy_key-2... se já existir. Com sudo, os
arquivos ficam com o seu usuário, para o gh conseguir ler.

Cadastre a privada no secret DEPLOY_SSH_KEY (os comandos gh mostrados já
apontam para ela) e depois apague o arquivo. Com --print-only, nada é
gravado: a chave aparece uma única vez na tela.`,
		Example: `  cd ~ && sudo deploy-tools key generate
  sudo deploy-tools key generate --out /root/ci_deploy_key
  sudo deploy-tools key generate --print-only`,
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if err := env.RequireRoot(); err != nil {
				return err
			}
			return doGenerateKey(env, g)
		},
	}
	cmd.Flags().StringVar(&g.comment, "comment", "", "comentário da chave (padrão: ci-deploy@<hostname>)")
	cmd.Flags().StringVar(&g.out, "out", "", "grava a chave privada neste arquivo (padrão: ./ci_deploy_key)")
	cmd.Flags().BoolVar(&g.printOnly, "print-only", false, "não grava: mostra a chave privada uma vez na tela")
	cmd.MarkFlagsMutuallyExclusive("out", "print-only")
	return cmd
}

// askKeySource is the TUI choice for the CI key: generate, file or skip.
func askKeySource(env *Env, allowSkip bool) (choice, path string, err error) {
	choice = "generate"
	opts := []huh.Option[string]{
		huh.NewOption("Gerar agora, aqui no servidor (salva em ./ci_deploy_key)", "generate"),
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
