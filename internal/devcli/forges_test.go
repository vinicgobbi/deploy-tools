package devcli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// withForges runs deploy-dev against a shared forges.yml.
func withForges(t *testing.T, file, input string, args ...string) (string, error) {
	t.Helper()
	return runWith(t, input, func(e *Env) { e.Commit, e.ForgesFile = ref, file }, args...)
}

func gitRemote(t *testing.T, dir, url string) {
	t.Helper()
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", url}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
}

func TestForgeAddListRemove(t *testing.T) {
	file := filepath.Join(t.TempDir(), "forges.yml")
	if _, err := withForges(t, file, "", "forge", "add", "empresa", "--kind", "gitea", "--url", "https://git.empresa.com/"); err != nil {
		t.Fatal(err)
	}
	if _, err := withForges(t, file, "", "forge", "add", "empresa", "--kind", "gitea", "--url", "https://x.y"); err == nil ||
		!strings.Contains(err.Error(), "--force") {
		t.Fatalf("duplicada: %v", err)
	}
	if _, err := withForges(t, file, "", "forge", "add", "github", "--kind", "gitea", "--url", "https://x.y"); err == nil {
		t.Fatal("o nome github é da instância embutida")
	}
	if _, err := withForges(t, file, "", "forge", "add", "x", "--kind", "gitlab", "--url", "https://x.y"); err == nil {
		t.Fatal("tipo inválido aceito")
	}
	out, err := withForges(t, file, "", "forge", "list")
	if err != nil || !strings.Contains(out, "empresa") || !strings.Contains(out, "https://git.empresa.com\n") ||
		!strings.Contains(out, "github") {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := withForges(t, file, "", "forge", "remove", "empresa"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(file); strings.Contains(string(b), "empresa") {
		t.Fatalf("não removeu:\n%s", b)
	}
	if _, err := withForges(t, file, "", "forge", "rm", "empresa"); err == nil {
		t.Fatal("remover inexistente deveria falhar")
	}
}

func TestInitPicksForgeFromRemote(t *testing.T) {
	file := filepath.Join(t.TempDir(), "forges.yml")
	if _, err := withForges(t, file, "", "forge", "add", "empresa", "--kind", "gitea", "--url", "https://git.empresa.com", "--runner", "linux"); err != nil {
		t.Fatal(err)
	}
	dir := reactProject(t)
	gitRemote(t, dir, "ssh://git@git.empresa.com:2222/acme/meu-front.git")
	out, err := withForges(t, file, "", "init", "--yes", "-C", dir)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	wf, err := os.ReadFile(filepath.Join(dir, ".gitea/workflows/deploy.yml"))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"para o Gitea", "runs-on: linux", "uses: vinicgobbi/deploy-tools@" + ref} {
		if !strings.Contains(string(wf), want) {
			t.Errorf("faltou %q:\n%s", want, wf)
		}
	}
	if !strings.Contains(out, "Configurações → Actions → Segredos") || strings.Contains(out, "Settings →") {
		t.Errorf("próximos passos do GitHub no Gitea:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, ".github")); err == nil {
		t.Error("gerou .github num repositório do Gitea")
	}
	if out, err := withForges(t, file, "", "check", dir); err != nil || !strings.Contains(out, "Workflows ok") {
		t.Fatalf("check: %v\n%s", err, out)
	}
	// --forge wins over the remote.
	if _, err := withForges(t, file, "", "init", "--yes", "--forge", "github", "-C", dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".github/workflows/deploy.yml")); err != nil {
		t.Fatal(err)
	}
}

func TestInitRefusesUnknownRemoteHost(t *testing.T) {
	file := filepath.Join(t.TempDir(), "forges.yml")
	dir := reactProject(t)
	gitRemote(t, dir, "git@git.desconhecido.com:acme/meu-front.git")
	_, err := withForges(t, file, "", "init", "--yes", "-C", dir)
	if err == nil || !strings.Contains(err.Error(), "git.desconhecido.com") || !strings.Contains(err.Error(), "forge add") {
		t.Fatalf("err = %v", err)
	}
	if _, err := withForges(t, file, "", "init", "--yes", "--forge", "nada", "-C", dir); err == nil {
		t.Fatal("--forge inexistente aceito")
	}
}

func TestGuidedManagesForges(t *testing.T) {
	file := filepath.Join(t.TempDir(), "forges.yml")
	t.Chdir(t.TempDir())
	isInstalled = func() bool { return true }
	t.Cleanup(func() { isInstalled = installed })
	steps := []string{
		// 4 = Cadastrar: nome, tipo (2 = Gitea), url, actions (padrão), runner.
		"4", "empresa", "2", "https://git.empresa.com", "", "linux",
		// 5 = Editar: 1 = empresa; tipo 3 = Forgejo; mantém o resto.
		"5", "1", "3", "https://code.empresa.com", "", "linux",
		// 3 = Listar.
		"3",
		// 9 = Sair.
		"9",
	}
	out, err := withForges(t, file, strings.Join(steps, "\n")+"\n")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	b, _ := os.ReadFile(file)
	for _, want := range []string{"name: empresa", "kind: forgejo", "url: https://code.empresa.com", "runner: linux"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("faltou %q em forges.yml:\n%s\n--- saída:\n%s", want, b, out)
		}
	}
	if strings.Count(string(b), "name:") != 1 {
		t.Errorf("editar duplicou a instância:\n%s", b)
	}
	// 6 = Remover: 1 = empresa; confirma.
	out, err = withForges(t, file, "6\n1\ny\n9\n")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if b, _ := os.ReadFile(file); strings.Contains(string(b), "empresa") {
		t.Errorf("não removeu:\n%s\n--- saída:\n%s", b, out)
	}
}
