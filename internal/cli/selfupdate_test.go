package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vinicgobbi/deploy-tools/internal/update"
)

// fakeRelease serves a latest release tag with a deploy-tools binary for
// this platform; hits counts the API calls.
func fakeRelease(t *testing.T, tag string, bin []byte) (url string, hits *int32) {
	t.Helper()
	hits = new(int32)
	sum := sha256.Sum256(bin)
	asset := update.AssetName("deploy-tools", runtime.GOOS, runtime.GOARCH)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			atomic.AddInt32(hits, 1)
			fmt.Fprintf(w, `{"tag_name":%q,"assets":[{"name":%q,"browser_download_url":"%s/bin"},{"name":%q,"browser_download_url":"%s/sum"}]}`,
				tag, asset, srv.URL, asset+".sha256", srv.URL)
		case "/bin":
			_, _ = w.Write(bin)
		case "/sum":
			fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), asset)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/latest", hits
}

func installedEnv(t *testing.T) (*Env, interface{ String() string }) {
	t.Helper()
	env, out := testEnv(t, "")
	env.Version = "v0.9.0"
	old := filepath.Join(t.TempDir(), "deploy-tools-linux-amd64")
	_ = os.WriteFile(old, []byte("old"), 0o755)
	if err := env.P.Install(old); err != nil {
		t.Fatal(err)
	}
	env.Self = env.Paths.BinaryPath()
	return env, out
}

func TestUpdateCommand(t *testing.T) {
	env, out := installedEnv(t)
	env.Updates.URL, _ = fakeRelease(t, "v0.10.0", []byte("new binary"))

	if err := runCLI(env, "update", "--check"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Nova versão: v0.10.0") {
		t.Fatalf("--check:\n%s", out)
	}
	if b, _ := os.ReadFile(env.Paths.BinaryPath()); string(b) != "old" {
		t.Fatal("--check must not install")
	}
	if err := runCLI(env, "update"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if b, _ := os.ReadFile(env.Paths.BinaryPath()); string(b) != "new binary" {
		t.Fatalf("binary = %q", b)
	}
	if target, _ := filepath.EvalSymlinks(env.Paths.WrapperPath()); target != env.Paths.BinaryPath() {
		t.Fatalf("deploy-ci -> %q", target)
	}
	if entries, _ := filepath.Glob(filepath.Join(env.Paths.BinDir, ".*.download")); len(entries) != 0 {
		t.Fatalf("download left behind: %v", entries)
	}

	env.Version = "v0.10.0"
	if err := runCLI(env, "update"); err != nil || !strings.Contains(out.String(), "Já está na versão mais recente") {
		t.Fatalf("up to date: %v\n%s", err, out)
	}
}

func TestAutoUpdateOncePerDay(t *testing.T) {
	env, out := installedEnv(t)
	var hits *int32
	env.Updates.URL, hits = fakeRelease(t, "v0.10.0", []byte("new binary"))
	env.AutoUpdate = true
	env.UpdateStamp = filepath.Join(t.TempDir(), "update-check")

	if err := runCLI(env, "app", "list"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(env.Paths.BinaryPath()); string(b) != "new binary" || !strings.Contains(out.String(), "atualizado: v0.9.0 → v0.10.0") {
		t.Fatalf("binary = %q\n%s", b, out)
	}
	_ = runCLI(env, "app", "list")
	_ = runCLI(env, "version") // never checks
	if *hits != 1 {
		t.Fatalf("GitHub hit %d times, want 1 (once a day)", *hits)
	}

	// Without root it only tells; dev builds never check.
	env2, out2 := installedEnv(t)
	env2.Updates.URL, _ = fakeRelease(t, "v0.10.0", []byte("x"))
	env2.AutoUpdate, env2.UpdateStamp = true, filepath.Join(t.TempDir(), "s")
	env2.RequireRoot = func() error { return fmt.Errorf("precisa de root") }
	_ = runCLI(env2, "app", "list")
	if !strings.Contains(out2.String(), "sudo deploy-tools update") {
		t.Fatalf("hint:\n%s", out2)
	}
	if b, _ := os.ReadFile(env2.Paths.BinaryPath()); string(b) != "old" {
		t.Fatal("installed without root")
	}
}

func TestInvokedFromPATHHidesInstall(t *testing.T) {
	for arg, want := range map[string]bool{"deploy-tools": true, "./deploy-tools-linux-amd64": false, "/usr/local/bin/deploy-tools": false} {
		if invokedFromPATH(arg) != want {
			t.Errorf("invokedFromPATH(%q) != %v", arg, want)
		}
	}
	labels := func(env *Env) string {
		var l []string
		for _, s := range tuiApp(env).Sections {
			for _, it := range s.Items {
				l = append(l, it.Label)
			}
		}
		return strings.Join(l, "|")
	}
	env, _ := testEnv(t, "")
	if !strings.Contains(labels(env), "Certificados HTTPS|Instalar/atualizar|Verificar atualizações") {
		t.Fatalf("loose copy must offer install: %s", labels(env))
	}
	env.FromPATH = true
	if strings.Contains(labels(env), "Instalar/atualizar") {
		t.Fatalf("from PATH must not offer install: %s", labels(env))
	}
}
