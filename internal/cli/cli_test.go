package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vinicgobbi/deploy-tools/internal/appconf"
	"github.com/vinicgobbi/deploy-tools/internal/config"
	"github.com/vinicgobbi/deploy-tools/internal/provision"
	"github.com/vinicgobbi/deploy-tools/internal/run"
	"github.com/vinicgobbi/deploy-tools/internal/ui"
)

// testEnv builds an Env rooted in a temp dir. With input != "", prompts are
// enabled in huh's accessible (line-based) mode, reading answers from it.
func testEnv(t *testing.T, input string) (*Env, *bytes.Buffer) {
	t.Helper()
	paths := config.ForRoot(t.TempDir())
	paths.DomainSuffix = ".example.com"
	for _, d := range []string{paths.AppsRoot, paths.SitesAvail, paths.SitesEnabled} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	u := &ui.UI{In: strings.NewReader(input), Out: &out, Interactive: input != "", Accessible: true}
	f := &run.Fake{Output: map[string]string{"apache2ctl -M": "rewrite_module"}}
	p := &provision.Provisioner{
		Paths: paths, Runner: f, Owner: provision.NoopOwner{}, Out: &out,
		Now: func() time.Time { return time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC) },
	}
	return &Env{Paths: paths, P: p, UI: u, Version: "test", RequireRoot: func() error { return nil }}, &out
}

func runCLI(env *Env, args ...string) error {
	root := NewRoot(env)
	root.SetArgs(args)
	root.SetOut(env.UI.Out)
	root.SetErr(env.UI.Out)
	return root.Execute()
}

func TestHelpForEveryCommand(t *testing.T) {
	env, _ := testEnv(t, "")
	var walk func(args []string)
	root := NewRoot(env)
	count := 0
	for _, c := range root.Commands() {
		walk = func(args []string) {
			if err := runCLI(env, append(args, "--help")...); err != nil {
				t.Errorf("%v --help: %v", args, err)
			}
			count++
		}
		walk([]string{c.Name()})
		for _, sub := range c.Commands() {
			walk([]string{c.Name(), sub.Name()})
		}
	}
	if count < 15 {
		t.Fatalf("only %d commands checked", count)
	}
}

func TestMissingFlagWithoutTTY(t *testing.T) {
	env, _ := testEnv(t, "")
	err := runCLI(env, "app", "create", "foo")
	if err == nil || !strings.Contains(err.Error(), "--type") || !strings.Contains(err.Error(), "exemplo:") {
		t.Fatalf("err = %v", err)
	}
	err = runCLI(env, "app", "create")
	if err == nil || !strings.Contains(err.Error(), "nome do app") {
		t.Fatalf("err = %v", err)
	}
}

func TestCreateByFlags(t *testing.T) {
	env, out := testEnv(t, "")
	if err := runCLI(env, "app", "create", "api", "--type", "laravel", "--migrate-on-deploy"); err != nil {
		t.Fatal(err)
	}
	c, ok, _ := appconf.Read(env.Paths.AppDir("api"))
	if !ok || c.Type != "laravel" || !c.LaravelMigrate {
		t.Fatalf("conf = %+v", c)
	}
	if !strings.Contains(out.String(), "sudo deploy-tools app enable api") {
		t.Fatalf("next steps missing:\n%s", out)
	}
}

// deployN fakes deploys of ids (in order) by creating the release dirs
// and pointing current at the last one.
func deployN(t *testing.T, env *Env, app string, ids ...string) {
	t.Helper()
	for _, id := range ids {
		dir := filepath.Join(env.Paths.ReleasesDir(app), id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(env.Paths.CurrentLink(app)); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if err := os.Symlink(dir, env.Paths.CurrentLink(app)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRollbackDefaultsToPrevious(t *testing.T) {
	env, _ := testEnv(t, "")
	if err := os.MkdirAll(env.Paths.AppDir("docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	deployN(t, env, "docs", "1", "2", "3")

	// Without a TTY a confirmation is required.
	if err := runCLI(env, "release", "rollback", "docs"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("err = %v", err)
	}
	if err := runCLI(env, "release", "rollback", "docs", "--yes"); err != nil {
		t.Fatal(err)
	}
	if got := env.P.Releases().CurrentID("docs"); got != "2" {
		t.Fatalf("current = %s, want 2", got)
	}
	if err := runCLI(env, "release", "rollback", "nope", "-y"); err == nil || !strings.Contains(err.Error(), "app create nope") {
		t.Fatalf("err = %v", err)
	}
}

func TestConfigDetectsType(t *testing.T) {
	env, _ := testEnv(t, "")
	_ = os.MkdirAll(env.Paths.AppDir("sis"), 0o755)
	deployN(t, env, "sis", "1")
	_ = os.WriteFile(filepath.Join(env.Paths.ReleasesDir("sis"), "1", "artisan"), nil, 0o644)
	if err := runCLI(env, "app", "config", "sis", "--migrate-on-deploy"); err != nil {
		t.Fatal(err)
	}
	c, ok, _ := appconf.Read(env.Paths.AppDir("sis"))
	if !ok || c.Type != "laravel" || !c.LaravelMigrate {
		t.Fatalf("conf = %+v", c)
	}
	if err := runCLI(env, "app", "config", "sis", "--no-migrate-on-deploy"); err != nil {
		t.Fatal(err)
	}
	c, _, _ = appconf.Read(env.Paths.AppDir("sis"))
	if c.LaravelMigrate || c.Type != "laravel" {
		t.Fatalf("conf after --no-migrate-on-deploy = %+v", c)
	}
}

// In accessible mode the TUI is a numbered menu with the same items:
// 1 Criar app, 2 Migrar, 3 Status, 4 Listar, 5 Rollback, 6 Configurar,
// 7 Ativar site, 8 Desativar site, 9 Chaves, 10 Adicionar chave,
// 13 Secrets, 14 Instalar, 15 Setup, 16 Certificados, 17 Sair.

func TestInteractiveCreate(t *testing.T) {
	// 1 = Criar app. Then name, type (1 = Laravel), domain (empty keeps
	// the default), migrations y, enable n, confirm y. The menu comes back
	// below the result: 17 = Sair.
	answers := strings.Join([]string{"1", "minha-api", "1", "", "y", "n", "y", "17"}, "\n") + "\n"
	env, out := testEnv(t, answers)
	env.UI.ClearScreen = true
	if err := runCLI(env); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	c, ok, err := appconf.Read(env.Paths.AppDir("minha-api"))
	if err != nil || !ok || c.Type != "laravel" || !c.LaravelMigrate {
		t.Fatalf("conf = %+v ok=%v err=%v\n%s", c, ok, err, out)
	}
	vhost, err := os.ReadFile(env.Paths.VhostFile("minha-api"))
	if err != nil || !strings.Contains(string(vhost), "ServerName minha-api.example.com") {
		t.Fatalf("vhost: %v\n%s", err, vhost)
	}
	// Menu, then the action screen; the result stays on screen with the
	// menu below it (no third clear, no "back to menu?" step).
	screens := strings.Split(out.String(), "\033[H\033[2J\033[3J")
	if len(screens) != 3 {
		t.Fatalf("got %d clears, want 2\n%q", len(screens)-1, out)
	}
	action := screens[2]
	created := strings.Index(action, "App minha-api criado")
	if !strings.HasPrefix(strings.TrimSpace(action), "Criar app") || created < 0 ||
		strings.LastIndex(action, "O que você quer fazer?") < created {
		t.Fatalf("action screen:\n%s", action)
	}
}

func TestInteractiveWithoutRootFailsBeforeAsking(t *testing.T) {
	env, out := testEnv(t, "1\n17\n")
	env.RequireRoot = func() error { return errors.New("precisa de root — rode com sudo") }
	if err := runCLI(env); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	s := out.String()
	if !strings.Contains(s, "Sem sudo") || !strings.Contains(s, "precisa de root") || strings.Contains(s, "Nome do app") {
		t.Fatalf("output:\n%s", s)
	}
}

func TestInteractiveRollback(t *testing.T) {
	// 5 = Rollback; app 1 (docs); release 1 (= "2", newest non-current);
	// confirm y; 17 = Sair.
	env, out := testEnv(t, strings.Join([]string{"5", "1", "1", "y", "17"}, "\n")+"\n")
	_ = os.MkdirAll(env.Paths.AppDir("docs"), 0o755)
	deployN(t, env, "docs", "1", "2", "3")
	if err := runCLI(env); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := env.P.Releases().CurrentID("docs"); got != "2" {
		t.Fatalf("current = %s\n%s", got, out)
	}
	if !strings.Contains(out.String(), "docs voltou para 2") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestStatusCard(t *testing.T) {
	env, out := testEnv(t, "")
	if err := runCLI(env, "app", "create", "api", "--type", "laravel"); err != nil {
		t.Fatal(err)
	}
	now := env.P.Now().In(time.Local)
	old, cur := now.Add(-50*time.Hour).Format("20060102150405")+"-db4e609", now.Add(-3*time.Hour).Format("20060102150405")+"-a99d021"
	deployN(t, env, "api", old, cur)
	out.Reset()
	if err := runCLI(env, "app", "status", "api"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"site desativado", "https://api.example.com", "Laravel · migrations a cada deploy: não",
		"a99d021 · há 3 h  (ativa)", "db4e609 · há 2 dias", "sudo deploy-tools app enable api"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("faltou %q:\n%s", want, out)
		}
	}
}

func TestInteractiveMigrate(t *testing.T) {
	// 2 = Migrar; app 1 (docs, the only unmanaged one: "api" is already
	// migrated and must not be offered); type 3 (Estático); confirm y;
	// 17 = Sair.
	env, out := testEnv(t, strings.Join([]string{"2", "1", "3", "y", "17"}, "\n")+"\n")
	_ = os.MkdirAll(env.Paths.AppDir("api"), 0o755)
	deployN(t, env, "api", "1")
	root := env.Paths.AppDir("docs")
	_ = os.MkdirAll(root, 0o755)
	_ = os.WriteFile(filepath.Join(root, "index.html"), []byte("hi"), 0o644)
	vhost := filepath.Join(env.Paths.SitesAvail, "docs.conf")
	_ = os.WriteFile(vhost, []byte("<VirtualHost *:443>\n  DocumentRoot "+root+"\n</VirtualHost>\n"), 0o644)
	_ = os.Symlink(vhost, filepath.Join(env.Paths.SitesEnabled, "docs.conf"))

	if err := runCLI(env); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if opts := selectOptions(out.String(), "Qual app migrar?"); len(opts) != 1 || !strings.Contains(opts[0], "docs") {
		t.Fatalf("opções = %q", opts)
	}
	if b, err := os.ReadFile(filepath.Join(env.Paths.CurrentLink("docs"), "index.html")); err != nil || string(b) != "hi" {
		t.Fatalf("index via current: %q %v\n%s", b, err, out)
	}
	if c, ok, _ := appconf.Read(root); !ok || c.Type != "static" {
		t.Fatalf("conf = %+v\n%s", c, out)
	}
}

// selectOptions returns the numbered options printed (accessible mode)
// right after the select titled title.
func selectOptions(out, title string) []string {
	i := strings.Index(out, title)
	if i < 0 {
		return nil
	}
	var opts []string
	for _, l := range strings.Split(out[i:], "\n")[1:] {
		if !strings.Contains(l, ". ") || strings.HasPrefix(l, "Enter") {
			break
		}
		opts = append(opts, l)
	}
	return opts
}

func TestInteractiveConfigOffersOnlyAppsWithoutConf(t *testing.T) {
	// 6 = Configurar; app 1 (semconf, the only one offered); type 3
	// (Estático); 17 = Sair.
	env, out := testEnv(t, strings.Join([]string{"6", "1", "3", "17"}, "\n")+"\n")
	in := env.UI.Interactive
	env.UI.Interactive = false
	if err := runCLI(env, "app", "create", "comconf", "--type", "react"); err != nil {
		t.Fatal(err)
	}
	env.UI.Interactive = in
	_ = os.MkdirAll(env.Paths.AppDir("legado"), 0o755) // not migrated
	_ = os.MkdirAll(env.Paths.AppDir("semconf"), 0o755)
	deployN(t, env, "semconf", "1")
	out.Reset()
	if err := runCLI(env); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if opts := selectOptions(out.String(), "Configurar qual app?"); len(opts) != 1 || !strings.Contains(opts[0], "semconf") {
		t.Fatalf("opções = %q\n%s", opts, out)
	}
	if c, ok, _ := appconf.Read(env.Paths.AppDir("semconf")); !ok || c.Type != "static" {
		t.Fatalf("conf = %+v\n%s", c, out)
	}
}

func TestInteractiveConfigNothingToDo(t *testing.T) {
	env, out := testEnv(t, "6\n17\n")
	_ = os.MkdirAll(env.Paths.AppDir("legado"), 0o755)
	if err := runCLI(env); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out.String(), "todos os apps migrados já têm .deploy.conf") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestInteractiveDisable(t *testing.T) {
	// 8 = Desativar site; app 1 (front, the only active site); confirm y;
	// 17 = Sair.
	env, out := testEnv(t, "8\n1\ny\n17\n")
	in := env.UI.Interactive
	env.UI.Interactive = false
	for _, app := range []string{"front", "parado"} {
		if err := runCLI(env, "app", "create", app, "--type", "react"); err != nil {
			t.Fatal(err)
		}
	}
	env.UI.Interactive = in
	_ = os.Symlink(env.Paths.VhostFile("front"), filepath.Join(env.Paths.SitesEnabled, "front.conf"))
	out.Reset()
	if err := runCLI(env); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if opts := selectOptions(out.String(), "Desativar o site de qual app?"); len(opts) != 1 || !strings.Contains(opts[0], "front") {
		t.Fatalf("opções = %q\n%s", opts, out)
	}
	calls := strings.Join(env.P.Runner.(*run.Fake).Strings(), "\n")
	if !strings.Contains(calls, "apache2ctl configtest\na2dissite front\napache2ctl configtest\nsystemctl reload apache2") {
		t.Fatalf("calls:\n%s", calls)
	}
}

func TestDomainCommands(t *testing.T) {
	env, out := testEnv(t, "")
	if err := runCLI(env, "app", "create", "loja", "--type", "static", "-d", "loja.example.org,www.loja.example.org"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if err := runCLI(env, "app", "domain", "add", "loja", "shop.example.org", "LOJA.example.org"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	out.Reset()
	if err := runCLI(env, "app", "domain", "list", "loja"); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "loja.example.org  (principal)\nwww.loja.example.org\nshop.example.org\n") {
		t.Fatalf("list:\n%s", got)
	}
	if err := runCLI(env, "app", "domain", "remove", "loja", "loja.example.org"); err != nil {
		t.Fatal(err)
	}
	vhost, _ := os.ReadFile(env.Paths.VhostFile("loja"))
	if !strings.Contains(string(vhost), "ServerName www.loja.example.org\n    ServerAlias shop.example.org\n") {
		t.Fatalf("vhost:\n%s", vhost)
	}
	out.Reset()
	if err := runCLI(env, "app", "status", "loja"); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "https://shop.example.org") || !strings.Contains(got, "autoassinado provisório") {
		t.Fatalf("status:\n%s", got)
	}
	if err := runCLI(env, "app", "cert", "loja", "--cert", "/x.crt"); err == nil {
		t.Fatal("--cert without --key must fail")
	}
}

func TestCompareVersions(t *testing.T) {
	for _, c := range []struct {
		a, b string
		cmp  int
		ok   bool
	}{
		{"v0.2.0", "v0.1.9", 1, true},
		{"v1.0.0", "v1.0.0-3-gabc1234", 0, true},
		{"v1.2.3", "v1.10.0", -1, true},
		{"dev", "v1.0.0", 0, false},
	} {
		if cmp, ok := compareVersions(c.a, c.b); cmp != c.cmp || ok != c.ok {
			t.Errorf("compareVersions(%q, %q) = %d, %v", c.a, c.b, cmp, ok)
		}
	}
}

func TestVersionShowsInstallState(t *testing.T) {
	env, out := testEnv(t, "")
	env.Version = "v0.2.0"
	env.Self = filepath.Join(t.TempDir(), "deploy-tools-linux-amd64")
	_ = os.WriteFile(env.Self, []byte("bin"), 0o755)

	if err := runCLI(env, "version"); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.HasPrefix(got, "deploy-tools v0.2.0\n") || !strings.Contains(got, "rodando solto de "+env.Self) ||
		!strings.Contains(got, "ainda não está instalado") {
		t.Fatalf("not installed:\n%s", got)
	}

	if err := env.P.Install(env.Self); err != nil {
		t.Fatal(err)
	}
	env.install = nil
	env.P.Runner.(*run.Fake).Output[env.Paths.BinaryPath()+" version"] = "deploy-tools v0.1.0\n"
	out.Reset()
	if err := runCLI(env, "server", "status"); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "Instalado: v0.1.0") || !strings.Contains(got, "Este é mais novo") {
		t.Fatalf("older installed:\n%s", got)
	}

	env.Self, env.install = env.Paths.BinaryPath(), nil
	out.Reset()
	_ = runCLI(env, "version")
	if got := out.String(); strings.Contains(got, "solto") || !strings.Contains(got, "Instalado em "+env.Paths.BinaryPath()+" (v0.2.0)") {
		t.Fatalf("running installed:\n%s", got)
	}
}

func TestKeyAddShowsSecrets(t *testing.T) {
	env, out := testEnv(t, "")
	env.Paths.DeployHost = "deploy.example.com"
	env.P.Paths = env.Paths
	_ = os.MkdirAll(env.Paths.SSHDir, 0o755)
	_ = os.WriteFile(filepath.Join(env.Paths.SSHDir, "ssh_host_ed25519_key.pub"), []byte("ssh-ed25519 AAAAhost root@srv\n"), 0o644)
	pub := filepath.Join(t.TempDir(), "minha_chave.pub")
	_ = os.WriteFile(pub, []byte("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl ci-deploy\n"), 0o644)

	if err := runCLI(env, "key", "add", pub); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	got := out.String()
	for _, want := range []string{
		"Secrets do GitHub", "Não compartilhe",
		"DEPLOY_HOST", "deploy.example.com", "DEPLOY_USER", "deploy",
		"arquivo minha_chave (a chave PRIVADA",
		"deploy.example.com ssh-ed25519 AAAAhost",
		"ci-deploy",
		"gh secret set DEPLOY_SSH_KEY -R <dono>/<repo> < minha_chave",
		"| gh secret set DEPLOY_HOST_KEY -R <dono>/<repo>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("faltou %q:\n%s", want, got)
		}
	}

	out.Reset()
	if err := runCLI(env, "key", "secrets", "--org", "acme", "--host", "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "gh secret set DEPLOY_HOST --org acme --visibility all --body '1.2.3.4'") ||
		!strings.Contains(got, "'1.2.3.4 ssh-ed25519 AAAAhost'") {
		t.Fatalf("key secrets:\n%s", got)
	}
}
