package provision

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vinicgobbi/deploy-tools/internal/apache"
	"github.com/vinicgobbi/deploy-tools/internal/appconf"
	"github.com/vinicgobbi/deploy-tools/internal/config"
	"github.com/vinicgobbi/deploy-tools/internal/run"
)

func newP(t *testing.T) (*Provisioner, *run.Fake) {
	t.Helper()
	paths := config.ForRoot(t.TempDir())
	paths.BaseDomains = []string{"example.com"}
	for _, d := range []string{paths.AppsRoot, paths.SitesAvail, paths.SitesEnabled} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f := &run.Fake{Output: map[string]string{"apache2ctl -M": "rewrite_module (shared)\n"}}
	var out bytes.Buffer
	return &Provisioner{
		Paths: paths, Runner: f, Owner: NoopOwner{}, Out: &out,
		Now: func() time.Time { return time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC) },
	}, f
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCreate(t *testing.T) {
	for _, typ := range []string{"laravel", "react", "static"} {
		t.Run(typ, func(t *testing.T) {
			p, f := newP(t)
			err := p.Create(CreateOptions{App: "app", Type: typ, MigrateOnDeploy: true, Enable: true})
			if err != nil {
				t.Fatal(err)
			}
			fi, err := os.Stat(p.Paths.ReleasesDir("app"))
			if err != nil || fi.Mode()&os.ModeSetgid == 0 {
				t.Fatalf("releases dir: %v %v", fi, err)
			}
			c, ok, err := appconf.Read(p.Paths.AppDir("app"))
			if err != nil || !ok || c.Type != typ || c.Migrate != (typ == "laravel") {
				t.Fatalf("conf = %+v %v %v", c, ok, err)
			}
			vhost := read(t, p.Paths.VhostFile("app"))
			if !strings.Contains(vhost, "ServerName app.example.com") {
				t.Fatalf("default domain missing:\n%s", vhost)
			}
			_, envErr := os.Stat(filepath.Join(p.Paths.SharedDir("app"), ".env"))
			if (typ == "laravel") != (envErr == nil) {
				t.Fatalf(".env presence wrong for %s: %v", typ, envErr)
			}
			calls := strings.Join(f.Strings(), "\n")
			if !strings.Contains(calls, "a2ensite app\napache2ctl configtest\nsystemctl reload apache2") {
				t.Fatalf("enable sequence missing:\n%s", calls)
			}
			// Second create fails without touching anything.
			if err := p.Create(CreateOptions{App: "app", Type: typ}); err == nil {
				t.Fatal("second create succeeded")
			}
		})
	}
}

func TestCreateRefusesExistingVhostBeforeCreatingDirs(t *testing.T) {
	p, _ := newP(t)
	write(t, p.Paths.VhostFile("app"), "x")
	if err := p.Create(CreateOptions{App: "app", Type: "static"}); err == nil {
		t.Fatal("expected error")
	}
	if exists(p.Paths.AppDir("app")) {
		t.Fatal("app dir created despite error")
	}
}

// failAfter makes configtest fail only once a command starting with cmd
// ran: the change broke the config, it was valid before.
func failAfter(f *run.Fake, cmd string) {
	broken := false
	f.Func = func(c run.Cmd) error {
		if strings.HasPrefix(c.String(), cmd) {
			broken = true
		}
		if broken && c.String() == "apache2ctl configtest" {
			return run.ErrFake
		}
		return nil
	}
}

func enableLink(t *testing.T, p *Provisioner, file string) {
	t.Helper()
	if err := os.Symlink(file, filepath.Join(p.Paths.SitesEnabled, filepath.Base(file))); err != nil {
		t.Fatal(err)
	}
}

func TestEnableTestsConfigBeforeAndAfter(t *testing.T) {
	p, f := newP(t)
	if err := p.Create(CreateOptions{App: "app", Type: "static"}); err != nil {
		t.Fatal(err)
	}
	f.Calls = nil
	if err := p.Enable("app"); err != nil {
		t.Fatal(err)
	}
	want := "apache2ctl configtest\na2ensite app\napache2ctl configtest\nsystemctl reload apache2"
	if got := strings.Join(f.Strings(), "\n"); got != want {
		t.Fatalf("calls:\n%s\nwant:\n%s", got, want)
	}
}

func TestEnableRollsBackOnBadConfig(t *testing.T) {
	p, f := newP(t)
	if err := p.Create(CreateOptions{App: "app", Type: "static"}); err != nil {
		t.Fatal(err)
	}
	failAfter(f, "a2ensite app")
	if err := p.Enable("app"); err == nil || errors.Is(err, apache.ErrConfigAlreadyBroken) {
		t.Fatalf("err = %v", err)
	}
	calls := strings.Join(f.Strings(), "\n")
	if !strings.Contains(calls, "a2dissite app") || strings.Contains(calls, "systemctl reload") {
		t.Fatalf("calls:\n%s", calls)
	}
}

func TestEnableRefusesAlreadyBrokenConfig(t *testing.T) {
	p, f := newP(t)
	if err := p.Create(CreateOptions{App: "app", Type: "static"}); err != nil {
		t.Fatal(err)
	}
	f.Fail = map[string]error{"apache2ctl configtest": run.ErrFake}
	f.Calls = nil
	if err := p.Enable("app"); !errors.Is(err, apache.ErrConfigAlreadyBroken) {
		t.Fatalf("err = %v", err)
	}
	if got := f.Strings(); len(got) != 1 {
		t.Fatalf("changed something with a broken config: %q", got)
	}
}

func TestEnableAlreadyEnabledIsNoop(t *testing.T) {
	p, f := newP(t)
	if err := p.Create(CreateOptions{App: "app", Type: "static"}); err != nil {
		t.Fatal(err)
	}
	enableLink(t, p, p.Paths.VhostFile("app"))
	f.Calls = nil
	if err := p.Enable("app"); err != nil || len(f.Calls) != 0 {
		t.Fatalf("err = %v, calls = %q", err, f.Strings())
	}
}

func TestDisableUsesSiteNameAndRollsBack(t *testing.T) {
	// A migrated app whose site is not named after it.
	p, f := newP(t)
	root := p.Paths.AppDir("sistema")
	write(t, filepath.Join(root, "releases", "1", "index.html"), "x")
	vhost := filepath.Join(p.Paths.SitesAvail, "sistema-ssl.conf")
	write(t, vhost, "<VirtualHost *:443>\n  DocumentRoot "+root+"/current\n</VirtualHost>\n")
	enableLink(t, p, vhost)

	if err := p.Disable("sistema"); err != nil {
		t.Fatal(err)
	}
	want := "apache2ctl configtest\na2dissite sistema-ssl\napache2ctl configtest\nsystemctl reload apache2"
	if got := strings.Join(f.Strings(), "\n"); got != want {
		t.Fatalf("calls:\n%s\nwant:\n%s", got, want)
	}

	f.Calls = nil
	failAfter(f, "a2dissite")
	if err := p.Disable("sistema"); err == nil {
		t.Fatal("expected error")
	}
	if calls := strings.Join(f.Strings(), "\n"); !strings.Contains(calls, "a2ensite sistema-ssl") {
		t.Fatalf("site not enabled again:\n%s", calls)
	}
}

func TestDisableAlreadyDisabledIsNoop(t *testing.T) {
	p, f := newP(t)
	if err := p.Create(CreateOptions{App: "app", Type: "static"}); err != nil {
		t.Fatal(err)
	}
	f.Calls = nil
	if err := p.Disable("app"); err != nil || len(f.Calls) != 0 {
		t.Fatalf("err = %v, calls = %q", err, f.Strings())
	}
	if err := p.Disable("nope"); err == nil {
		t.Fatal("app without site: expected error")
	}
}

func TestCreateEnableRefusesBrokenConfigBeforeCreating(t *testing.T) {
	p, f := newP(t)
	f.Fail = map[string]error{"apache2ctl configtest": run.ErrFake}
	err := p.Create(CreateOptions{App: "app", Type: "static", Enable: true})
	if !errors.Is(err, apache.ErrConfigAlreadyBroken) {
		t.Fatalf("err = %v", err)
	}
	if exists(p.Paths.AppDir("app")) || exists(p.Paths.VhostFile("app")) {
		t.Fatal("app created despite broken config")
	}
}

func TestMigrateRefusesBrokenConfigBeforeTouching(t *testing.T) {
	p, f := newP(t)
	legacyLaravel(t, p)
	pl, err := p.PlanMigrate(MigrateOptions{App: "api", Type: "laravel"})
	if err != nil {
		t.Fatal(err)
	}
	before := read(t, filepath.Join(p.Paths.SitesAvail, "api.conf"))
	f.Fail = map[string]error{"apache2ctl configtest": run.ErrFake}
	if err := p.Migrate(pl); !errors.Is(err, apache.ErrConfigAlreadyBroken) {
		t.Fatalf("err = %v", err)
	}
	if exists(p.Paths.CurrentLink("api")) || read(t, filepath.Join(p.Paths.SitesAvail, "api.conf")) != before {
		t.Fatal("migration touched files with a broken config")
	}
}

const liveLaravelVhost = `<VirtualHost *:443>
    ServerName api.example.com
    DocumentRoot /ROOT/api/public
</VirtualHost>
<Directory /ROOT/api/public>
    Options -Indexes
    AllowOverride All
</Directory>
`

// legacyLaravel builds /var/www/api as a plain, live Laravel checkout with
// an enabled vhost.
func legacyLaravel(t *testing.T, p *Provisioner) {
	t.Helper()
	root := p.Paths.AppDir("api")
	write(t, filepath.Join(root, "artisan"), "#!")
	write(t, filepath.Join(root, "public", "index.php"), "<?php")
	write(t, filepath.Join(root, ".env"), "APP_KEY=secret")
	write(t, filepath.Join(root, "storage", "logs", "laravel.log"), "log")
	vhost := filepath.Join(p.Paths.SitesAvail, "api.conf")
	write(t, vhost, strings.ReplaceAll(liveLaravelVhost, "/ROOT", p.Paths.AppsRoot))
	if err := os.Symlink(vhost, filepath.Join(p.Paths.SitesEnabled, "api.conf")); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateLaravel(t *testing.T) {
	p, f := newP(t)
	legacyLaravel(t, p)
	pl, err := p.PlanMigrate(MigrateOptions{App: "api", Type: "laravel"})
	if err != nil {
		t.Fatal(err)
	}
	root := p.Paths.AppDir("api")
	if pl.OldDocRoot != root+"/public" || pl.NewDocRoot != root+"/current/public" {
		t.Fatalf("plan = %+v", pl)
	}
	if pl.ReleaseID != "20261005120000-pre-migracao" {
		t.Fatalf("release id = %s", pl.ReleaseID)
	}
	if err := p.Migrate(pl); err != nil {
		t.Fatal(err)
	}

	// Content served through the new DocumentRoot.
	if got := read(t, filepath.Join(pl.NewDocRoot, "index.php")); got != "<?php" {
		t.Fatalf("index.php via current = %q", got)
	}
	// State moved to shared/ and linked back.
	if got := read(t, filepath.Join(p.Paths.SharedDir("api"), ".env")); got != "APP_KEY=secret" {
		t.Fatalf("shared .env = %q", got)
	}
	if got := read(t, filepath.Join(root, "current", "storage", "logs", "laravel.log")); got != "log" {
		t.Fatalf("storage via current = %q", got)
	}
	// The temporary "current -> ." is gone from the release.
	rel := filepath.Join(p.Paths.ReleasesDir("api"), pl.ReleaseID)
	if exists(filepath.Join(rel, "current")) {
		t.Fatal("legacy current -> . left inside the release")
	}
	vhost := read(t, filepath.Join(p.Paths.SitesAvail, "api.conf"))
	if !strings.Contains(vhost, "DocumentRoot "+root+"/current/public") ||
		!strings.Contains(vhost, "<Directory "+root+"/current/public/>") ||
		!strings.Contains(vhost, "Options -Indexes +FollowSymLinks") {
		t.Fatalf("vhost not repointed:\n%s", vhost)
	}
	if !exists(filepath.Join(p.Paths.SitesAvail, "api.conf.bak-"+pl.ReleaseID)) {
		t.Fatal("vhost backup missing")
	}
	c, ok, _ := appconf.Read(root)
	if !ok || c.Type != "laravel" {
		t.Fatalf("conf = %+v %v", c, ok)
	}
	if got := p.Releases().CurrentID("api"); got != pl.ReleaseID {
		t.Fatalf("current = %q", got)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(p.Paths.AppsRoot, ".*")); len(leftovers) > 0 {
		t.Fatalf("staging left behind: %v", leftovers)
	}
	if !strings.Contains(strings.Join(f.Strings(), "\n"), "apache2ctl configtest\nsystemctl reload apache2") {
		t.Fatal("apache not reloaded")
	}

	// Running it again is refused.
	if _, err := p.PlanMigrate(MigrateOptions{App: "api", Type: "laravel"}); err == nil {
		t.Fatal("second migration planned")
	}
}

func TestMigrateConfigtestFailureLeavesEverythingAsIs(t *testing.T) {
	p, f := newP(t)
	legacyLaravel(t, p)
	vhostPath := filepath.Join(p.Paths.SitesAvail, "api.conf")
	before := read(t, vhostPath)
	f.Fail = map[string]error{"apache2ctl configtest": run.ErrFake}

	pl, err := p.PlanMigrate(MigrateOptions{App: "api", Type: "laravel"})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Migrate(pl); err == nil {
		t.Fatal("expected error")
	}
	if read(t, vhostPath) != before {
		t.Fatal("vhost not restored")
	}
	root := p.Paths.AppDir("api")
	for _, name := range []string{"current", "releases", "shared"} {
		if exists(filepath.Join(root, name)) {
			t.Fatalf("%s created despite failure", name)
		}
	}
	if read(t, filepath.Join(root, ".env")) != "APP_KEY=secret" {
		t.Fatal(".env moved")
	}
}

func TestMigrateNeedsExactlyOneVhost(t *testing.T) {
	p, _ := newP(t)
	write(t, filepath.Join(p.Paths.AppDir("docs"), "index.html"), "hi")
	if _, err := p.PlanMigrate(MigrateOptions{App: "docs", Type: "static"}); err == nil ||
		!strings.Contains(err.Error(), "--vhost") {
		t.Fatalf("err = %v", err)
	}
	// Explicit --vhost (even if not enabled) works.
	v := filepath.Join(p.Paths.SitesAvail, "docs.conf")
	write(t, v, "DocumentRoot "+p.Paths.AppDir("docs")+"/\n")
	pl, err := p.PlanMigrate(MigrateOptions{App: "docs", Type: "static", Vhost: v})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Migrate(pl); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(p.Paths.CurrentLink("docs"), "index.html")); got != "hi" {
		t.Fatalf("got %q", got)
	}
}

func TestAddKeyIdempotent(t *testing.T) {
	p, _ := newP(t)
	pub := filepath.Join(t.TempDir(), "k.pub")
	write(t, pub, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl ci-deploy\n")
	write(t, p.Paths.AuthorizedKeys(), "ssh-rsa AAAAB3Nza other") // no trailing newline
	for i := 0; i < 2; i++ {
		if err := p.AddKey(pub); err != nil {
			t.Fatal(err)
		}
	}
	got := read(t, p.Paths.AuthorizedKeys())
	if strings.Count(got, "AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl") != 1 {
		t.Fatalf("key duplicated or missing:\n%s", got)
	}
	want := `command="` + p.Paths.WrapperPath() + `",no-port-forwarding,no-X11-forwarding,no-agent-forwarding,no-pty ssh-ed25519 `
	if !strings.Contains(got, "other\n"+want) {
		t.Fatalf("unexpected content:\n%s", got)
	}
	keys, _ := p.Keys()
	if len(keys) != 2 || keys[1].Command != p.Paths.WrapperPath() || keys[1].Comment != "ci-deploy" {
		t.Fatalf("keys = %+v", keys)
	}

	priv := filepath.Join(t.TempDir(), "k")
	write(t, priv, "-----BEGIN OPENSSH PRIVATE KEY-----\nxxx\n")
	if err := p.AddKey(priv); err == nil {
		t.Fatal("private key accepted")
	}
}

func TestInstallReplacesBashWrapper(t *testing.T) {
	p, _ := newP(t)
	self := filepath.Join(t.TempDir(), "deploy-tools")
	write(t, self, "BINARY")
	write(t, p.Paths.WrapperPath(), "#!/usr/bin/env bash\nold")
	for i := 0; i < 2; i++ { // idempotent
		if err := p.Install(self); err != nil {
			t.Fatal(err)
		}
	}
	if got := read(t, p.Paths.WrapperPath()); got != "BINARY" {
		t.Fatalf("deploy-ci resolves to %q", got)
	}
	if got := read(t, p.Paths.WrapperPath()+".bak"); !strings.Contains(got, "old") {
		t.Fatalf("backup = %q", got)
	}
	if fi, _ := os.Stat(p.Paths.BinaryPath()); fi.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v", fi.Mode())
	}
}

func TestCreateSymfony(t *testing.T) {
	p, _ := newP(t)
	if err := p.Create(CreateOptions{App: "loja", Type: "symfony", MigrateOnDeploy: true}); err != nil {
		t.Fatal(err)
	}
	sh := p.Paths.SharedDir("loja")
	if got := read(t, filepath.Join(sh, ".env.local")); got != "APP_ENV=prod\nAPP_DEBUG=0\n" {
		t.Fatalf(".env.local = %q", got)
	}
	if fi, err := os.Stat(filepath.Join(sh, "var-log")); err != nil || !fi.IsDir() {
		t.Fatalf("shared/var-log: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sh, ".env")); !os.IsNotExist(err) {
		t.Fatal("Symfony must not get a shared .env (it ships its own defaults)")
	}
	vhost := read(t, p.Paths.VhostFile("loja"))
	if !strings.Contains(vhost, "DocumentRoot "+filepath.Join(p.Paths.CurrentLink("loja"), "public")) ||
		!strings.Contains(vhost, "FallbackResource /index.php") {
		t.Fatalf("vhost:\n%s", vhost)
	}
	if c, ok, err := appconf.Read(p.Paths.AppDir("loja")); err != nil || !ok || c.Type != "symfony" || !c.Migrate {
		t.Fatalf("conf = %+v %v %v", c, ok, err)
	}
}

func TestCreateAngularIsSPA(t *testing.T) {
	p, _ := newP(t)
	if err := p.Create(CreateOptions{App: "painel", Type: "angular"}); err != nil {
		t.Fatal(err)
	}
	vhost := read(t, p.Paths.VhostFile("painel"))
	if !strings.Contains(vhost, "RewriteRule ^ index.html [L]") || strings.Contains(vhost, "/public") {
		t.Fatalf("vhost:\n%s", vhost)
	}
}

// recOwner records every chown, so tests can check ownership without root.
type recOwner struct{ paths []string }

func (r *recOwner) Chown(path, _, _ string, _ bool) error {
	r.paths = append(r.paths, path)
	return nil
}

func TestCreateLaravelSharesEveryStorageLevel(t *testing.T) {
	p, _ := newP(t)
	owner := &recOwner{}
	p.Owner = owner
	if err := p.Create(CreateOptions{App: "app", Type: "laravel"}); err != nil {
		t.Fatal(err)
	}
	sh := p.Paths.SharedDir("app")
	for _, d := range []string{"storage/app", "storage/app/private", "storage/framework", "storage/framework/views"} {
		path := filepath.Join(sh, d)
		fi, err := os.Stat(path)
		if err != nil || fi.Mode()&os.ModeSetgid == 0 || fi.Mode().Perm() != 0o775 {
			t.Errorf("%s: %v %v (quer 2775)", d, fi.Mode(), err)
		}
		if !slices.Contains(owner.paths, path) {
			t.Errorf("%s sem chown deploy:www-data", d)
		}
	}
}

func TestConfigureRepairsLaravelStorage(t *testing.T) {
	p, _ := newP(t)
	if err := p.Create(CreateOptions{App: "app", Type: "laravel"}); err != nil {
		t.Fatal(err)
	}
	// As left by versions before v0.16: a parent created by MkdirAll.
	broken := filepath.Join(p.Paths.SharedDir("app"), "storage/app")
	_ = os.RemoveAll(filepath.Join(broken, "private"))
	_ = os.Chmod(broken, 0o755)

	owner := &recOwner{}
	p.Owner = owner
	if err := p.Configure("app", appconf.Conf{Type: "laravel"}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(broken)
	if err != nil || fi.Mode()&os.ModeSetgid == 0 || fi.Mode().Perm() != 0o775 || !slices.Contains(owner.paths, broken) {
		t.Fatalf("storage/app não foi reparado: %v %v", fi.Mode(), err)
	}
	if _, err := os.Stat(filepath.Join(broken, "private")); err != nil {
		t.Fatalf("storage/app/private: %v", err)
	}
}
