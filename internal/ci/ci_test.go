package ci

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vinicgobbi/deploy-tools/internal/config"
	"github.com/vinicgobbi/deploy-tools/internal/release"
	"github.com/vinicgobbi/deploy-tools/internal/run"
)

func tarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

type result struct {
	stdout, stderr string
	code           int
}

// goImpl runs a request through ci.Main.
func goImpl(t *testing.T, p config.Paths, cmd string, stdin []byte) result {
	t.Helper()
	var out, errb bytes.Buffer
	m := &release.Manager{Paths: p, Runner: run.Exec{}, Out: &out, Err: &errb}
	code := Main(m, cmd, bytes.NewReader(stdin))
	return result{out.String(), errb.String(), code}
}

// bashImpl runs the same request through the original bash wrapper (kept in
// testdata), re-rooted at p.AppsRoot.
func bashImpl(t *testing.T, p config.Paths, cmd string, stdin []byte) result {
	t.Helper()
	src, err := os.ReadFile("testdata/legacy-deploy.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := strings.Replace(string(src), `APPS_ROOT="/var/www"`, `APPS_ROOT="`+p.AppsRoot+`"`, 1)
	path := filepath.Join(t.TempDir(), "deploy-ci")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	c := exec.Command("bash", path)
	c.Env = append(os.Environ(), "SSH_ORIGINAL_COMMAND="+cmd)
	c.Stdin = bytes.NewReader(stdin)
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr = &out, &errb
	code := 0
	if err := c.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatal(err)
		}
		code = ee.ExitCode()
	}
	return result{out.String(), errb.String(), code}
}

func newRoot(t *testing.T) config.Paths {
	t.Helper()
	p := config.ForRoot(t.TempDir())
	for _, app := range []string{"docs", "api"} {
		if err := os.MkdirAll(p.AppDir(app), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// api has persistent state, including a dotfile.
	if err := os.MkdirAll(filepath.Join(p.SharedDir("api"), "storage"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.SharedDir("api"), ".env"), []byte("APP_KEY=x\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	return p
}

// treeOf describes an app dir as "path -> link" / "path" lines, so the two
// implementations' resulting layouts can be compared.
func treeOf(t *testing.T, root string) string {
	t.Helper()
	var lines []string
	_ = filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if rel == "." || strings.HasSuffix(rel, ".deploy.lock") {
			return nil
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			target, _ := os.Readlink(path)
			lines = append(lines, rel+" -> "+strings.TrimPrefix(target, root))
			return nil
		}
		lines = append(lines, rel)
		return nil
	})
	return strings.Join(lines, "\n")
}

// TestCompatWithLegacyBash replays the same request sequence against the Go
// implementation and the original bash script and requires identical
// stdout, stderr, exit codes and resulting directory trees.
func TestCompatWithLegacyBash(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	site := tarball(t, map[string]string{"index.html": "<h1>hi</h1>", "storage/.gitkeep": ""})
	steps := []struct {
		cmd   string
		stdin []byte
	}{
		{"", nil},
		{"shell", nil},
		{"deploy", nil},
		{"deploy ../etc 1", nil},
		{"deploy nope 1", nil},
		{"deploy docs ..", nil},
		{"deploy docs a/b", nil},
		{"deploy docs 20260101000001-aaa", site},
		{"deploy docs 20260101000001-aaa", site},
		{"deploy docs 20260101000002-bbb", site},
		{"deploy docs 20260101000003-ccc", site},
		{"deploy docs 20260101000004-ddd", site},
		{"list docs", nil},
		{"rollback docs 20260101000003-ccc", nil},
		{"rollback docs ..", nil},
		{"rollback docs .", nil},
		{"rollback docs 20260101000001-aaa", nil},
		{"list docs", nil},
		{"deploy docs 20260101000005-eee", site},
		{"list docs", nil},
		{"deploy api 20260101000001-aaa", site},
		{"list api", nil},
		{"list nope", nil},
	}

	goP, bashP := newRoot(t), newRoot(t)
	for _, s := range steps {
		g := goImpl(t, goP, s.cmd, s.stdin)
		b := bashImpl(t, bashP, s.cmd, s.stdin)
		// Paths embedded in messages differ only by temp root.
		b.stderr = strings.ReplaceAll(b.stderr, bashP.AppsRoot, goP.AppsRoot)
		if g != b {
			t.Errorf("%q:\n go:   %+v\n bash: %+v", s.cmd, g, b)
		}
	}
	for _, app := range []string{"docs", "api"} {
		if g, b := treeOf(t, goP.AppDir(app)), treeOf(t, bashP.AppDir(app)); g != b {
			t.Errorf("%s tree differs:\n go:\n%s\n bash:\n%s", app, g, b)
		}
	}
}

func TestCorruptTarballLeavesCurrentAlone(t *testing.T) {
	p := newRoot(t)
	site := tarball(t, map[string]string{"index.html": "v1"})
	if r := goImpl(t, p, "deploy docs 1", site); r.code != 0 {
		t.Fatalf("first deploy: %+v", r)
	}
	r := goImpl(t, p, "deploy docs 2", []byte("not a tarball"))
	if r.code == 0 {
		t.Fatal("corrupt tarball deploy succeeded")
	}
	if _, err := os.Stat(filepath.Join(p.ReleasesDir("docs"), "2")); !os.IsNotExist(err) {
		t.Fatalf("partial release left behind: %v", err)
	}
	cur, _ := os.Readlink(p.CurrentLink("docs"))
	if filepath.Base(cur) != "1" {
		t.Fatalf("current moved to %s", cur)
	}
	// The id is free for a retry.
	if r := goImpl(t, p, "deploy docs 2", site); r.code != 0 {
		t.Fatalf("retry: %+v", r)
	}
}
