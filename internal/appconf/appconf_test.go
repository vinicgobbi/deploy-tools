package appconf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if _, ok, err := Read(dir); ok || err != nil {
		t.Fatalf("missing conf: ok=%v err=%v", ok, err)
	}
	want := Conf{Type: "laravel", Migrate: true}
	if err := Write(dir, want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := Read(dir)
	if err != nil || !ok || got != want {
		t.Fatalf("got %+v ok=%v err=%v", got, ok, err)
	}
	if fi, _ := os.Stat(Path(dir)); fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode %v", fi.Mode())
	}
}

func TestScheduleRoundTripAndOnlyForLaravel(t *testing.T) {
	dir := t.TempDir()
	want := Conf{Type: "laravel", Migrate: true, Schedule: true}
	if err := Write(dir, want); err != nil {
		t.Fatal(err)
	}
	if got, _, err := Read(dir); err != nil || got != want {
		t.Fatalf("got %+v err=%v", got, err)
	}
	// Other types never get the key, so older deploy-ci versions (which
	// reject unknown keys) keep reading their conf.
	if err := Write(dir, Conf{Type: "react"}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(Path(dir)); strings.Contains(string(b), "SCHEDULE") {
		t.Fatalf("SCHEDULE gravado para react:\n%s", b)
	}
	// A conf written before the key existed reads as scheduler off.
	_ = os.WriteFile(Path(dir), []byte("TYPE=laravel\nMIGRATE=yes\n"), 0o644)
	if got, _, err := Read(dir); err != nil || got.Schedule || !got.Migrate {
		t.Fatalf("conf antiga: %+v err=%v", got, err)
	}
	_ = os.WriteFile(Path(dir), []byte("TYPE=laravel\nSCHEDULE=talvez\n"), 0o644)
	if _, _, err := Read(dir); err == nil {
		t.Fatal("SCHEDULE inválido deveria falhar")
	}
}

func TestReadRejectsGarbage(t *testing.T) {
	for _, body := range []string{
		"TYPE=php\n",
		"LARAVEL_MIGRATE=maybe\n",
		"MIGRATE=sim\n",
		"FOO=bar\n",
		"just text\n",
	} {
		dir := t.TempDir()
		_ = os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o644)
		if _, _, err := Read(dir); err == nil {
			t.Errorf("%q accepted", body)
		}
	}
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, FileName), []byte("# c\n\nTYPE=\"react\"\n"), 0o644)
	if c, ok, err := Read(dir); err != nil || !ok || c.Type != "react" {
		t.Fatalf("comments/quotes: %+v %v %v", c, ok, err)
	}
}

func TestReadsLegacyMigrateKey(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, FileName), []byte("TYPE=laravel\nLARAVEL_MIGRATE=yes\n"), 0o644)
	if c, ok, err := Read(dir); err != nil || !ok || !c.Migrate {
		t.Fatalf("legacy key: %+v %v %v", c, ok, err)
	}
	if err := Write(dir, Conf{Type: "symfony", Migrate: true}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(Path(dir)); !strings.Contains(string(b), "MIGRATE=yes") || strings.Contains(string(b), "LARAVEL_") {
		t.Fatalf("written:\n%s", b)
	}
}

func TestDetectType(t *testing.T) {
	for typ, files := range map[string][]string{
		"laravel":     {"artisan"},
		"symfony":     {"bin/console", "public/index.php"},
		"angular":     {"index.html", "main-ABC123.js", "polyfills-XYZ.js"},
		"react":       {"index.html", "assets/x.js"},
		"angular-ssr": {"browser/index.csr.html", "server/server.mjs"},
		"static":      {"index.html"},
	} {
		dir := t.TempDir()
		for _, f := range files {
			_ = os.MkdirAll(filepath.Dir(filepath.Join(dir, f)), 0o755)
			_ = os.WriteFile(filepath.Join(dir, f), nil, 0o644)
		}
		want := strings.TrimSuffix(typ, "-ssr")
		if got := DetectType(dir); got != want {
			t.Errorf("DetectType(%v) = %s, want %s", files, got, want)
		}
		if got := DetectSSR(dir); got != (typ == "angular-ssr") {
			t.Errorf("DetectSSR(%v) = %v", files, got)
		}
	}
}

func TestSSRRoundTripAndChecks(t *testing.T) {
	dir := t.TempDir()
	want := Conf{Type: "angular", SSR: true, Port: 4001}
	if err := Write(dir, want); err != nil {
		t.Fatal(err)
	}
	if got, _, err := Read(dir); err != nil || got != want {
		t.Fatalf("got %+v err=%v", got, err)
	}
	if b, _ := os.ReadFile(Path(dir)); !strings.Contains(string(b), "SSR=yes\nPORT=4001\n") {
		t.Fatalf("written:\n%s", b)
	}
	// Angular without SSR keeps the conf readable by older deploy-ci.
	if err := Write(dir, Conf{Type: "angular"}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(Path(dir)); strings.Contains(string(b), "SSR") || strings.Contains(string(b), "PORT") {
		t.Fatalf("SSR gravado sem SSR:\n%s", b)
	}
	for _, c := range []Conf{
		{Type: "react", SSR: true, Port: 4000},
		{Type: "angular", SSR: true},
		{Type: "angular", SSR: true, Port: 80},
		{Type: "angular", SSR: true, Port: 40000},
	} {
		if err := Write(dir, c); err == nil {
			t.Errorf("Write(%+v) aceito", c)
		}
	}
	for _, body := range []string{
		"TYPE=angular\nSSR=yes\n",
		"TYPE=react\nSSR=yes\nPORT=4000\n",
		"TYPE=angular\nSSR=yes\nPORT=abc\n",
		"TYPE=angular\nSSR=talvez\nPORT=4000\n",
	} {
		_ = os.WriteFile(Path(dir), []byte(body), 0o644)
		if _, _, err := Read(dir); err == nil {
			t.Errorf("%q aceito", body)
		}
	}
}
