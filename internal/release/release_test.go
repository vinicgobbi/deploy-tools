package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/vinicgobbi/deploy-tools/internal/appconf"
	"github.com/vinicgobbi/deploy-tools/internal/config"
	"github.com/vinicgobbi/deploy-tools/internal/run"
)

func site(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "artisan", Mode: 0o755, Size: 2})
	_, _ = tw.Write([]byte("#!"))
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func setup(t *testing.T, conf *appconf.Conf) (*Manager, *run.Fake) {
	t.Helper()
	p := config.ForRoot(t.TempDir())
	if err := os.MkdirAll(p.SharedDir("api"), 0o755); err != nil {
		t.Fatal(err)
	}
	if conf != nil {
		if err := appconf.Write(p.AppDir("api"), *conf); err != nil {
			t.Fatal(err)
		}
	}
	f := &run.Fake{Passthrough: map[string]bool{"tar": true}}
	var out bytes.Buffer
	return &Manager{Paths: p, Runner: f, Out: &out, Err: &out}, f
}

func phpCalls(f *run.Fake) []string {
	var out []string
	for _, c := range f.Calls {
		if c.Name == "php" {
			out = append(out, c.String())
		}
	}
	return out
}

func TestHooks(t *testing.T) {
	base := []string{
		"php artisan storage:link",
		"php artisan config:cache",
		"php artisan route:cache",
		"php artisan view:cache",
	}
	cases := []struct {
		name string
		conf *appconf.Conf
		want []string
	}{
		{"no conf = legacy, no hooks", nil, nil},
		{"static", &appconf.Conf{Type: "static"}, nil},
		{"react", &appconf.Conf{Type: "react"}, nil},
		{"laravel", &appconf.Conf{Type: "laravel"}, base},
		{"laravel+migrate", &appconf.Conf{Type: "laravel", Migrate: true}, append(base, "php artisan migrate --force")},
		{"angular", &appconf.Conf{Type: "angular"}, nil},
		{"symfony", &appconf.Conf{Type: "symfony"}, []string{"php bin/console cache:clear --env=prod --no-debug"}},
		{"symfony+migrate", &appconf.Conf{Type: "symfony", Migrate: true}, []string{
			"php bin/console cache:clear --env=prod --no-debug",
			"php bin/console doctrine:migrations:migrate --no-interaction --allow-no-migration --env=prod --no-debug",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, f := setup(t, tc.conf)
			if err := m.Deploy("api", "1", bytes.NewReader(site(t))); err != nil {
				t.Fatal(err)
			}
			if got := phpCalls(f); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("hooks = %q, want %q", got, tc.want)
			}
			for _, c := range f.Calls {
				if c.Name == "php" && c.Dir != filepath.Join(m.Paths.ReleasesDir("api"), "1") {
					t.Fatalf("hook ran in %s", c.Dir)
				}
			}
		})
	}
}

func TestFailingHookAbortsBeforeSwap(t *testing.T) {
	m, f := setup(t, &appconf.Conf{Type: "laravel", Migrate: true})
	if err := m.Deploy("api", "1", bytes.NewReader(site(t))); err != nil {
		t.Fatal(err)
	}
	f.Fail = map[string]error{"php artisan migrate": run.ErrFake}
	if err := m.Deploy("api", "2", bytes.NewReader(site(t))); err == nil {
		t.Fatal("expected failure")
	}
	if got := m.CurrentID("api"); got != "1" {
		t.Fatalf("current = %q, want 1", got)
	}
	if _, err := os.Stat(filepath.Join(m.Paths.ReleasesDir("api"), "2")); !os.IsNotExist(err) {
		t.Fatal("failed release not removed")
	}
}

func TestPruneSparesCurrent(t *testing.T) {
	m, _ := setup(t, nil)
	for _, id := range []string{"1", "2", "3", "4"} {
		if err := m.Deploy("api", id, bytes.NewReader(site(t))); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Rollback("api", "3"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"5", "6"} {
		if err := m.Deploy("api", id, bytes.NewReader(site(t))); err != nil {
			t.Fatal(err)
		}
	}
	rels, err := m.List("api")
	if err != nil {
		t.Fatal(err)
	}
	want := []Release{{"6", true}, {"5", false}, {"4", false}}
	if !reflect.DeepEqual(rels, want) {
		t.Fatalf("releases = %+v, want %+v", rels, want)
	}
	// Roll back to an old one, then deploy: the rolled-back-to release is
	// never pruned while current.
	if err := m.Rollback("api", "4"); err != nil {
		t.Fatal(err)
	}
	if err := m.Prune("api"); err != nil {
		t.Fatal(err)
	}
	rels, _ = m.List("api")
	want = []Release{{"6", false}, {"5", false}, {"4", true}}
	if !reflect.DeepEqual(rels, want) {
		t.Fatalf("after rollback = %+v, want %+v", rels, want)
	}
}

func TestLinkSharedIncludesDotfiles(t *testing.T) {
	m, _ := setup(t, nil)
	sh := m.Paths.SharedDir("api")
	_ = os.WriteFile(filepath.Join(sh, ".env"), []byte("X=1"), 0o640)
	_ = os.MkdirAll(filepath.Join(sh, "storage"), 0o755)
	if err := m.Deploy("api", "1", bytes.NewReader(site(t))); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(m.Paths.CurrentLink("api"), ".env"))
	if err != nil || string(b) != "X=1" {
		t.Fatalf(".env via current: %q, %v", b, err)
	}
	link, _ := os.Readlink(filepath.Join(m.Paths.ReleasesDir("api"), "1", "storage"))
	if link != "../../shared/storage" {
		t.Fatalf("storage link = %q", link)
	}
}

func TestSymfonySharesOnlyVarLog(t *testing.T) {
	m, _ := setup(t, &appconf.Conf{Type: "symfony"})
	sh := m.Paths.SharedDir("api")
	_ = os.MkdirAll(filepath.Join(sh, "var-log"), 0o775)
	_ = os.WriteFile(filepath.Join(sh, ".env.local"), []byte("APP_ENV=prod\n"), 0o640)
	if err := m.Deploy("api", "1", bytes.NewReader(site(t))); err != nil {
		t.Fatal(err)
	}
	rel := filepath.Join(m.Paths.ReleasesDir("api"), "1")
	if got, _ := os.Readlink(filepath.Join(rel, "var", "log")); got != "../../../shared/var-log" {
		t.Fatalf("var/log -> %q", got)
	}
	if fi, err := os.Stat(filepath.Join(rel, "var", "log")); err != nil || !fi.IsDir() {
		t.Fatalf("var/log does not resolve: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(rel, "var-log")); !os.IsNotExist(err) {
		t.Fatal("var-log also linked at the release root")
	}
	if got, _ := os.Readlink(filepath.Join(rel, ".env.local")); got != "../../shared/.env.local" {
		t.Fatalf(".env.local -> %q", got)
	}
	if fi, err := os.Lstat(filepath.Join(rel, "var")); err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatal("var/ must be a real directory of the release (cache stays per release)")
	}
}
