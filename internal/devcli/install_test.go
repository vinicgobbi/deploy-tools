package devcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestInstaller(t *testing.T, goos, shell, pathEnv string) *Installer {
	t.Helper()
	home := t.TempDir()
	self := filepath.Join(t.TempDir(), "deploy-dev-linux-amd64")
	if err := os.WriteFile(self, []byte("BIN v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"LOCALAPPDATA": filepath.Join(home, "AppData", "Local")}
	return &Installer{GOOS: goos, Home: home, Self: self, PathEnv: pathEnv, Shell: shell,
		Getenv: func(k string) string { return env[k] }}
}

func TestDefaultDirAndName(t *testing.T) {
	l := newTestInstaller(t, "linux", "/bin/zsh", "")
	if got := l.DefaultDir(); got != filepath.Join(l.Home, ".local", "bin") {
		t.Errorf("linux dir = %s", got)
	}
	w := newTestInstaller(t, "windows", "", "")
	if got := w.DefaultDir(); got != filepath.Join(w.Home, "AppData", "Local", "Programs", "deploy-dev") {
		t.Errorf("windows dir = %s", got)
	}
	if w.binName() != "deploy-dev.exe" || l.binName() != "deploy-dev" {
		t.Error("binName")
	}
}

func TestCopyInstallsAndUpdates(t *testing.T) {
	in := newTestInstaller(t, "linux", "/bin/zsh", "")
	dir := in.DefaultDir()
	dst, already, err := in.Copy(dir)
	if err != nil || already {
		t.Fatalf("copy: %v already=%v", err, already)
	}
	if b, _ := os.ReadFile(dst); string(b) != "BIN v1" {
		t.Fatalf("conteúdo = %q", b)
	}
	if fi, _ := os.Stat(dst); fi.Mode().Perm() != 0o755 {
		t.Fatalf("modo = %v", fi.Mode())
	}
	// Newer download replaces it.
	_ = os.WriteFile(in.Self, []byte("BIN v2"), 0o644)
	if _, _, err := in.Copy(dir); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "BIN v2" {
		t.Fatalf("não atualizou: %q", b)
	}
	// Running the installed binary's own install is a no-op.
	in.Self = dst
	if _, already, _ := in.Copy(dir); !already {
		t.Fatal("deveria detectar que já é o instalado")
	}
	if leftovers, _ := filepath.Glob(dst + ".*"); len(leftovers) > 0 {
		t.Fatalf("sobras: %v", leftovers)
	}
}

func TestCopyWindowsMovesRunningBinaryAside(t *testing.T) {
	in := newTestInstaller(t, "windows", "", "")
	dir := in.DefaultDir()
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "deploy-dev.exe"), []byte("old"), 0o755)
	dst, _, err := in.Copy(dir)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "BIN v1" {
		t.Fatalf("dst = %q", b)
	}
	if b, _ := os.ReadFile(dst + ".old"); string(b) != "old" {
		t.Fatalf(".old = %q", b)
	}
}

func TestOnPath(t *testing.T) {
	in := newTestInstaller(t, "linux", "/bin/zsh", "")
	dir := in.DefaultDir()
	in.PathEnv = "/usr/bin:" + dir + "/"
	if !in.OnPath(dir) {
		t.Error("deveria estar no PATH")
	}
	in.PathEnv = "/usr/bin:~/.local/bin"
	if !in.OnPath(dir) {
		t.Error("~ deveria expandir")
	}
	in.PathEnv = "/usr/bin"
	if in.OnPath(dir) {
		t.Error("não deveria estar no PATH")
	}
}

func TestShellRCAndIdempotentAppend(t *testing.T) {
	cases := map[string]struct{ goos, shell, file, line string }{
		"zsh":       {"linux", "/usr/bin/zsh", ".zshrc", `export PATH="$HOME/.local/bin:$PATH"`},
		"bash":      {"linux", "/bin/bash", ".bashrc", `export PATH="$HOME/.local/bin:$PATH"`},
		"bash-mac":  {"darwin", "/bin/bash", ".bash_profile", `export PATH="$HOME/.local/bin:$PATH"`},
		"fish":      {"linux", "/usr/bin/fish", ".config/fish/config.fish", "fish_add_path ~/.local/bin"},
		"sem shell": {"darwin", "", ".zshrc", `export PATH="$HOME/.local/bin:$PATH"`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			in := newTestInstaller(t, c.goos, c.shell, "")
			file, line := in.ShellRC(in.DefaultDir())
			if file != filepath.Join(in.Home, c.file) || line != c.line {
				t.Fatalf("got %s / %s", file, line)
			}
			_ = os.MkdirAll(filepath.Dir(file), 0o755)
			_ = os.WriteFile(file, []byte("alias x=y"), 0o644) // sem \n final
			for i := 0; i < 2; i++ {
				if _, err := AddToShellRC(file, line); err != nil {
					t.Fatal(err)
				}
			}
			b, _ := os.ReadFile(file)
			if strings.Count(string(b), line) != 1 || !strings.HasPrefix(string(b), "alias x=y\n") {
				t.Fatalf("rc:\n%s", b)
			}
		})
	}
}
