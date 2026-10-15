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
		"laravel": {"artisan"},
		"symfony": {"bin/console", "public/index.php"},
		"angular": {"index.html", "main-ABC123.js", "polyfills-XYZ.js"},
		"react":   {"index.html", "assets/x.js"},
		"static":  {"index.html"},
	} {
		dir := t.TempDir()
		for _, f := range files {
			_ = os.MkdirAll(filepath.Dir(filepath.Join(dir, f)), 0o755)
			_ = os.WriteFile(filepath.Join(dir, f), nil, 0o644)
		}
		if got := DetectType(dir); got != typ {
			t.Errorf("DetectType(%v) = %s, want %s", files, got, typ)
		}
	}
}
