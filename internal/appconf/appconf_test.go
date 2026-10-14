package appconf

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if _, ok, err := Read(dir); ok || err != nil {
		t.Fatalf("missing conf: ok=%v err=%v", ok, err)
	}
	want := Conf{Type: "laravel", LaravelMigrate: true}
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
