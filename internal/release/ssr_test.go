package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/vinicgobbi/deploy-tools/internal/appconf"
	"github.com/vinicgobbi/deploy-tools/internal/run"
)

// ssrBuild is an Angular SSR dist/<project>: browser/ and server/.
func ssrBuild(t *testing.T, withServer bool) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	files := []string{"browser/index.csr.html"}
	if withServer {
		files = append(files, "server/server.mjs")
	}
	for _, name := range files {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: 1})
		_, _ = tw.Write([]byte("x"))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func ssrSetup(t *testing.T) (*Manager, *run.Fake, *[]int) {
	t.Helper()
	m, f := setup(t, &appconf.Conf{Type: "angular", SSR: true, Port: 4001})
	var checked []int
	m.SSRReady = func(port int) error { checked = append(checked, port); return nil }
	return m, f, &checked
}

func restarts(f *run.Fake) int {
	n := 0
	for _, c := range f.Strings() {
		if c == "sudo -n /usr/bin/systemctl restart deploy-tools-api.service" {
			n++
		}
	}
	return n
}

func TestDeploySSRRestartsAndChecksPort(t *testing.T) {
	m, f, checked := ssrSetup(t)
	if err := m.Deploy("api", "1", bytes.NewReader(ssrBuild(t, true))); err != nil {
		t.Fatal(err)
	}
	if restarts(f) != 1 || len(*checked) != 1 || (*checked)[0] != 4001 {
		t.Fatalf("restarts=%d checked=%v calls=%v", restarts(f), *checked, f.Strings())
	}
	if m.CurrentID("api") != "1" {
		t.Fatalf("current = %q", m.CurrentID("api"))
	}
}

func TestDeploySSRFailureRestoresPrevious(t *testing.T) {
	m, f, _ := ssrSetup(t)
	if err := m.Deploy("api", "1", bytes.NewReader(ssrBuild(t, true))); err != nil {
		t.Fatal(err)
	}
	// The new release never opens the port; the old one does.
	calls := 0
	m.SSRReady = func(int) error {
		calls++
		if calls == 1 {
			return errors.New("nothing listening")
		}
		return nil
	}
	f.Calls = nil
	err := m.Deploy("api", "2", bytes.NewReader(ssrBuild(t, true)))
	if err == nil || !strings.Contains(err.Error(), "journalctl -u deploy-tools-api.service") {
		t.Fatalf("err = %v", err)
	}
	if m.CurrentID("api") != "1" {
		t.Fatalf("current = %q, want 1", m.CurrentID("api"))
	}
	if restarts(f) != 2 {
		t.Fatalf("restarts = %d (nova + volta da anterior)", restarts(f))
	}
	if _, err := os.Stat(m.Paths.ReleasesDir("api") + "/2"); !os.IsNotExist(err) {
		t.Fatal("a release que não subiu ficou em disco")
	}
}

func TestFirstSSRDeployFailureLeavesNoCurrent(t *testing.T) {
	m, f, _ := ssrSetup(t)
	f.Fail = map[string]error{"sudo": run.ErrFake}
	if err := m.Deploy("api", "1", bytes.NewReader(ssrBuild(t, true))); err == nil {
		t.Fatal("deveria falhar")
	}
	if _, err := os.Lstat(m.Paths.CurrentLink("api")); !os.IsNotExist(err) {
		t.Fatal("current ficou apontando para uma release apagada")
	}
}

func TestDeploySSRRefusesBrowserOnlyPackage(t *testing.T) {
	m, f, _ := ssrSetup(t)
	err := m.Deploy("api", "1", bytes.NewReader(ssrBuild(t, false)))
	if err == nil || !strings.Contains(err.Error(), "server/server.mjs") {
		t.Fatalf("err = %v", err)
	}
	if restarts(f) != 0 || m.CurrentID("api") != "" {
		t.Fatal("não deveria trocar nem reiniciar")
	}
}

func TestRollbackSSRRestarts(t *testing.T) {
	m, f, _ := ssrSetup(t)
	for _, id := range []string{"1", "2"} {
		if err := m.Deploy("api", id, bytes.NewReader(ssrBuild(t, true))); err != nil {
			t.Fatal(err)
		}
	}
	f.Calls = nil
	if err := m.Rollback("api", "1"); err != nil {
		t.Fatal(err)
	}
	if restarts(f) != 1 || m.CurrentID("api") != "1" {
		t.Fatalf("restarts=%d current=%s", restarts(f), m.CurrentID("api"))
	}
	m.SSRReady = func(int) error { return errors.New("down") }
	if err := m.Rollback("api", "2"); err == nil {
		t.Fatal("rollback com servidor fora do ar deveria falhar")
	}
	if m.CurrentID("api") != "1" {
		t.Fatalf("current = %s, want 1", m.CurrentID("api"))
	}
}

func TestNonSSRDeployNeverRestarts(t *testing.T) {
	m, f := setup(t, &appconf.Conf{Type: "angular"})
	if err := m.Deploy("api", "1", bytes.NewReader(ssrBuild(t, true))); err != nil {
		t.Fatal(err)
	}
	if restarts(f) != 0 {
		t.Fatal("reiniciou um app sem SSR")
	}
}
