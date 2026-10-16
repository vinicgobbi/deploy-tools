package devcli

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
	"testing"

	"github.com/vinicgobbi/deploy-tools/internal/update"
)

func TestUpdateReplacesInstalledDeployDev(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("layout de pastas do Windows")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	installed := filepath.Join(home, ".local", "bin", "deploy-dev")
	_ = os.MkdirAll(filepath.Dir(installed), 0o755)
	_ = os.WriteFile(installed, []byte("old"), 0o755)

	bin := []byte("new deploy-dev")
	sum := sha256.Sum256(bin)
	asset := update.AssetName("deploy-dev", runtime.GOOS, runtime.GOARCH)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			fmt.Fprintf(w, `{"tag_name":"v0.10.0","assets":[{"name":%q,"browser_download_url":"%s/bin"},{"name":%q,"browser_download_url":"%s/sum"}]}`,
				asset, srv.URL, asset+".sha256", srv.URL)
		case "/bin":
			_, _ = w.Write(bin)
		case "/sum":
			fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), asset)
		}
	}))
	defer srv.Close()

	out, err := runWith(t, "", func(e *Env) { e.Version = "v0.9.0"; e.Updates.URL = srv.URL + "/latest" }, "update")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if b, _ := os.ReadFile(installed); string(b) != "new deploy-dev" || !strings.Contains(out, "v0.9.0 → v0.10.0") {
		t.Fatalf("installed = %q\n%s", b, out)
	}

	_ = os.Remove(installed)
	if out, err := runWith(t, "", func(e *Env) { e.Version = "v0.9.0"; e.Updates.URL = srv.URL + "/latest" }, "update"); err == nil {
		t.Fatalf("not installed must fail:\n%s", out)
	}
}

func TestGuidedHidesInstallFromPATH(t *testing.T) {
	titles := func(env *Env) string {
		var l []string
		for _, s := range devApp(env, t.TempDir()).Sections {
			l = append(l, s.Title)
		}
		return strings.Join(l, "|")
	}
	env := &Env{Version: "v0.9.0"}
	if got := titles(env); got != "Projeto|Instâncias|Instalação|Esta ferramenta" {
		t.Fatalf("loose copy: %s", got)
	}
	env.FromPATH = true
	if got := titles(env); got != "Projeto|Instâncias|Esta ferramenta" {
		t.Fatalf("from PATH: %s", got)
	}
}
