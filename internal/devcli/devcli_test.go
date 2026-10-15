package devcli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vinicgobbi/deploy-tools/internal/project"
	"github.com/vinicgobbi/deploy-tools/internal/ui"
)

const ref = "66ac5741e772bfd2c2afb58b1d36527da520f3f6"

func run(t *testing.T, input string, commit string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	env := &Env{
		UI:      &ui.UI{In: strings.NewReader(input), Out: &out, Interactive: input != "", Accessible: true},
		Version: "v1.2.0", Commit: commit,
	}
	root := NewRoot(env)
	root.SetArgs(args)
	root.SetOut(&out)
	root.SetErr(&out)
	err := root.Execute()
	return out.String(), err
}

func reactProject(t *testing.T) string {
	dir := filepath.Join(t.TempDir(), "meu-front")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"scripts":{"build":"vite build"}}`), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "vite.config.ts"), nil, 0o644)
	_ = os.WriteFile(filepath.Join(dir, ".env.example"), []byte("VITE_API=x\n"), 0o644)
	return dir
}

func TestInitYesThenCheckPasses(t *testing.T) {
	dir := reactProject(t)
	out, err := run(t, "", ref, "init", "--yes", "-C", dir)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "Crie as variáveis do repositório") || !strings.Contains(out, "VITE_API") {
		t.Fatalf("next steps:\n%s", out)
	}
	wf, _ := os.ReadFile(filepath.Join(dir, ".github/workflows/deploy.yml"))
	if !strings.Contains(string(wf), "APP_NAME: meu-front") || !strings.Contains(string(wf), "uses: vinicgobbi/deploy-tools@"+ref+" # v1.2.0") {
		t.Fatalf("workflow:\n%s", wf)
	}
	if out, err := run(t, "", ref, "check", dir); err != nil || !strings.Contains(out, "Workflows ok") {
		t.Fatalf("check: %v\n%s", err, out)
	}
	// Second init refuses to overwrite without --force.
	if _, err := run(t, "", ref, "init", "--yes", "-C", dir); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("err = %v", err)
	}
}

func TestInitInteractiveOverridesDetection(t *testing.T) {
	dir := reactProject(t)
	// 2 = Ajustar antes de gerar; app, type (1=Laravel, 2=Symfony, keep React = 3),
	// branch, commitizen n, then React group: pm (2=npm), node, lint n, vars.
	answers := strings.Join([]string{"2", "meu-front", "3", "develop", "n", "2", "22", "n", "VITE_A, VITE_B"}, "\n") + "\n"
	out, err := run(t, answers, ref, "init", "-C", dir)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	wf, _ := os.ReadFile(filepath.Join(dir, ".github/workflows/deploy.yml"))
	for _, want := range []string{"APP_NAME: meu-front", "- develop", "npm ci", "npm run build", `node-version: "22"`, "VITE_A: ${{ vars.VITE_A }}", "VITE_B"} {
		if !strings.Contains(string(wf), want) {
			t.Errorf("faltou %q:\n%s", want, wf)
		}
	}
	if strings.Contains(string(wf), "commitizen") {
		t.Error("commitizen presente apesar de recusado")
	}
}

func TestInitWithoutReleaseCommitFails(t *testing.T) {
	_, err := run(t, "", "", "init", "--yes", "-C", reactProject(t))
	if err == nil || !strings.Contains(err.Error(), "release") {
		t.Fatalf("err = %v", err)
	}
}

func TestCheckExitCodes(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".github/workflows"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, ".github/workflows/x.yml"), []byte("steps:\n  - uses: actions/checkout@v4\n"), 0o644)
	if _, err := run(t, "", ref, "check", dir); err != errFindings {
		t.Fatalf("erro deve falhar: %v", err)
	}
	_ = os.WriteFile(filepath.Join(dir, ".github/workflows/x.yml"),
		[]byte("steps:\n  - uses: actions/checkout@"+project.Checkout.SHA+"\n"), 0o644) // sem comentário = aviso
	if _, err := run(t, "", ref, "check", dir); err != nil {
		t.Fatalf("aviso não deve falhar sem --strict: %v", err)
	}
	if _, err := run(t, "", ref, "check", "--strict", dir); err != errFindings {
		t.Fatalf("--strict deve falhar com aviso: %v", err)
	}
}

func TestInitInteractiveShowsDetectionFirst(t *testing.T) {
	dir := reactProject(t)
	// 1 = Sim, gerar com estas opções.
	out, err := run(t, "1\n", ref, "init", "-C", dir)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"Detectei isto no projeto", "meu-front", "React", "VITE_API", "Gerado"} {
		if !strings.Contains(out, want) {
			t.Errorf("faltou %q:\n%s", want, out)
		}
	}
	// 3 = Cancelar leaves the file alone.
	if _, err := run(t, "3\n", ref, "init", "-C", dir); err != ui.ErrAborted {
		t.Fatalf("err = %v", err)
	}
}

func TestGuidedMenu(t *testing.T) {
	dir := reactProject(t)
	t.Chdir(dir)
	isInstalled = func() bool { return true }
	t.Cleanup(func() { isInstalled = installed })
	// 1 = Gerar workflow; 1 = Sim (detecção); 2 = Checar; 4 = Sair.
	out, err := run(t, "1\n1\n2\n4\n", ref)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, ".github/workflows/deploy.yml")); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"meu-front", "Gerado", "Workflows ok"} {
		if !strings.Contains(out, want) {
			t.Errorf("faltou %q:\n%s", want, out)
		}
	}
}

// runWith is run with a hook to adjust the Env first.
func runWith(t *testing.T, input string, adjust func(*Env), args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	env := &Env{
		UI:      &ui.UI{In: strings.NewReader(input), Out: &out, Interactive: input != "", Accessible: true},
		Version: "v1.2.0",
	}
	adjust(env)
	root := NewRoot(env)
	root.SetArgs(args)
	root.SetOut(&out)
	root.SetErr(&out)
	err := root.Execute()
	return out.String(), err
}
