package provision

import (
	"os"
	"strings"
	"testing"

	"github.com/vinicgobbi/deploy-tools/internal/appconf"
	"github.com/vinicgobbi/deploy-tools/internal/run"
)

// allPortsFree makes the port search independent of what runs on the
// test machine.
func allPortsFree(t *testing.T) {
	t.Helper()
	old := portFree
	portFree = func(int) bool { return true }
	t.Cleanup(func() { portFree = old })
}

func newSSR(t *testing.T) (*Provisioner, *run.Fake) {
	t.Helper()
	allPortsFree(t)
	p, f := newP(t)
	f.Output["node -e"] = "/usr/bin/node"
	return p, f
}

func TestCreateAngularSSR(t *testing.T) {
	p, f := newSSR(t)
	if err := p.Create(CreateOptions{App: "loja", Type: "angular", SSR: true, Enable: true}); err != nil {
		t.Fatal(err)
	}
	c, ok, err := appconf.Read(p.Paths.AppDir("loja"))
	if err != nil || !ok || !c.SSR || c.Port != FirstSSRPort {
		t.Fatalf("conf = %+v %v %v", c, ok, err)
	}
	vhost := read(t, p.Paths.VhostFile("loja"))
	for _, want := range []string{
		"DocumentRoot " + p.Paths.CurrentLink("loja") + "/browser",
		"RewriteRule ^/(.*)$ http://127.0.0.1:4000/$1 [P,L]",
	} {
		if !strings.Contains(vhost, want) {
			t.Errorf("vhost sem %q:\n%s", want, vhost)
		}
	}
	if strings.Contains(vhost, "index.html") {
		t.Errorf("vhost SSR com fallback de SPA:\n%s", vhost)
	}

	unit := read(t, p.Paths.UnitFile("loja"))
	for _, want := range []string{
		"User=www-data",
		"ExecStart=/usr/bin/env PORT=4000 /usr/bin/node " + p.Paths.CurrentLink("loja") + "/server/server.mjs",
		"EnvironmentFile=-" + p.Paths.SharedDir("loja") + "/.env",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit sem %q:\n%s", want, unit)
		}
	}
	sudoers := read(t, p.Paths.SudoersFile("loja"))
	if !strings.Contains(sudoers, "deploy ALL=(root) NOPASSWD: /usr/bin/systemctl restart deploy-tools-loja.service\n") {
		t.Errorf("sudoers:\n%s", sudoers)
	}
	if fi, _ := os.Stat(p.Paths.SudoersFile("loja")); fi.Mode().Perm() != 0o440 {
		t.Errorf("sudoers mode %v", fi.Mode())
	}

	calls := strings.Join(f.Strings(), "\n")
	for _, want := range []string{"visudo -cf", "systemctl daemon-reload", "systemctl enable deploy-tools-loja.service",
		"a2enmod proxy\n", "a2enmod proxy_http", "a2enmod headers"} {
		if !strings.Contains(calls+"\n", want) {
			t.Errorf("faltou %q em:\n%s", want, calls)
		}
	}

	// The next SSR app gets the next port.
	if err := p.Create(CreateOptions{App: "blog", Type: "angular", SSR: true}); err != nil {
		t.Fatal(err)
	}
	if c, _, _ := appconf.Read(p.Paths.AppDir("blog")); c.Port != FirstSSRPort+1 {
		t.Fatalf("porta do 2º app = %d", c.Port)
	}
	if err := p.Create(CreateOptions{App: "outro", Type: "angular", SSR: true, Port: FirstSSRPort}); err == nil ||
		!strings.Contains(err.Error(), "loja") {
		t.Fatalf("porta repetida: %v", err)
	}
}

func TestCreateSSRChecksBeforeCreating(t *testing.T) {
	p, f := newSSR(t)
	if err := p.Create(CreateOptions{App: "app", Type: "react", SSR: true}); err == nil {
		t.Fatal("SSR em React aceito")
	}
	delete(f.Output, "node -e")
	f.Fail = map[string]error{"node -e": run.ErrFake}
	if err := p.Create(CreateOptions{App: "app", Type: "angular", SSR: true}); err == nil || !strings.Contains(err.Error(), "Node.js") {
		t.Fatalf("sem node: %v", err)
	}
	if exists(p.Paths.AppDir("app")) {
		t.Fatal("criou a pasta mesmo sem o node")
	}
	f.Fail = nil
	f.Output["node -e"] = "/root/.nvm/versions/node/v22/bin/node"
	if err := p.Create(CreateOptions{App: "app", Type: "angular", SSR: true}); err == nil || !strings.Contains(err.Error(), "nvm") {
		t.Fatalf("node em /root: %v", err)
	}
}

func TestSudoersRefusedByVisudoIsNotInstalled(t *testing.T) {
	p, f := newSSR(t)
	f.Fail = map[string]error{"visudo": run.ErrFake}
	if err := p.Create(CreateOptions{App: "app", Type: "angular", SSR: true}); err == nil {
		t.Fatal("deveria falhar")
	}
	if exists(p.Paths.SudoersFile("app")) {
		t.Fatal("sudoers instalado sem passar no visudo")
	}
	entries, _ := os.ReadDir(p.Paths.SudoersDir)
	if len(entries) != 0 {
		t.Fatalf("sobrou temporário: %v", entries)
	}
}

func TestConfigureKeepsSSRAndRefusesTypeChange(t *testing.T) {
	p, f := newSSR(t)
	if err := p.Create(CreateOptions{App: "loja", Type: "angular", SSR: true}); err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(p.Paths.ReleasesDir("loja"), 0o755)
	if err := p.Configure("loja", appconf.Conf{Type: "react", SSR: true, Port: 4000}); err == nil {
		t.Fatal("troca de tipo de um app SSR aceita")
	}
	// A unit removed by hand is put back.
	_ = os.Remove(p.Paths.UnitFile("loja"))
	f.Calls = nil
	if err := p.Configure("loja", appconf.Conf{Type: "angular", SSR: true, Port: 4000}); err != nil {
		t.Fatal(err)
	}
	if !exists(p.Paths.UnitFile("loja")) {
		t.Fatal("unit não recriada")
	}
	// Turning SSR off (conf without it) stops and removes the service.
	if err := p.syncSSR("loja", appconf.Conf{Type: "angular"}, ""); err != nil {
		t.Fatal(err)
	}
	if exists(p.Paths.UnitFile("loja")) || exists(p.Paths.SudoersFile("loja")) {
		t.Fatal("unit ou sudoers ficaram")
	}
	if !strings.Contains(strings.Join(f.Strings(), "\n"), "systemctl disable --now deploy-tools-loja.service") {
		t.Fatalf("calls: %v", f.Strings())
	}
}
