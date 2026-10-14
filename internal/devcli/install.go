package devcli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

// Installer copies the running binary into a per-user directory on PATH.
// Everything OS-dependent is a field so it can be tested on any platform.
type Installer struct {
	GOOS    string
	Home    string
	Self    string // path of the running binary
	PathEnv string // $PATH
	Shell   string // $SHELL (unix)
	Getenv  func(string) string
}

func newInstaller() (*Installer, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if real, err := filepath.EvalSymlinks(self); err == nil {
		self = real
	}
	return &Installer{
		GOOS: runtime.GOOS, Home: home, Self: self,
		PathEnv: os.Getenv("PATH"), Shell: os.Getenv("SHELL"), Getenv: os.Getenv,
	}, nil
}

func (in *Installer) binName() string {
	if in.GOOS == "windows" {
		return "deploy-dev.exe"
	}
	return "deploy-dev"
}

// DefaultDir is a per-user bin directory that needs no admin rights.
func (in *Installer) DefaultDir() string {
	if in.GOOS == "windows" {
		base := in.Getenv("LOCALAPPDATA")
		if base == "" {
			base = filepath.Join(in.Home, "AppData", "Local")
		}
		return filepath.Join(base, "Programs", "deploy-dev")
	}
	return filepath.Join(in.Home, ".local", "bin")
}

func (in *Installer) OnPath(dir string) bool {
	want := filepath.Clean(dir)
	for _, d := range filepath.SplitList(in.PathEnv) {
		d = strings.Replace(d, "~", in.Home, 1)
		if in.GOOS == "windows" {
			if strings.EqualFold(filepath.Clean(d), want) {
				return true
			}
		} else if filepath.Clean(d) == want {
			return true
		}
	}
	return false
}

// Copy installs Self as dir/<binName>. The new file is written next to the
// target and renamed over it; on Windows a running deploy-dev.exe cannot be
// overwritten, so it is moved aside first.
func (in *Installer) Copy(dir string) (dst string, already bool, err error) {
	dst = filepath.Join(dir, in.binName())
	if a, b := cleanReal(in.Self), cleanReal(dst); a == b {
		return dst, true, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", false, err
	}
	tmp := dst + ".new"
	if err := copyFile(in.Self, tmp); err != nil {
		_ = os.Remove(tmp)
		return "", false, err
	}
	if in.GOOS == "windows" {
		old := dst + ".old"
		_ = os.Remove(old)
		if _, err := os.Stat(dst); err == nil {
			if err := os.Rename(dst, old); err != nil {
				return "", false, err
			}
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		return "", false, err
	}
	return dst, false, nil
}

func cleanReal(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return filepath.Clean(p)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, 0o755)
}

// ShellRC returns the startup file and the line that puts dir on PATH for
// the user's shell (unix only).
func (in *Installer) ShellRC(dir string) (file, line string) {
	shown := dir
	if rel, err := filepath.Rel(in.Home, dir); err == nil && !strings.HasPrefix(rel, "..") {
		shown = "$HOME/" + filepath.ToSlash(rel)
	}
	switch filepath.Base(in.Shell) {
	case "fish":
		return filepath.Join(in.Home, ".config", "fish", "config.fish"), "fish_add_path " + strings.Replace(shown, "$HOME", "~", 1)
	case "bash":
		rc := ".bashrc"
		if in.GOOS == "darwin" {
			rc = ".bash_profile"
		}
		return filepath.Join(in.Home, rc), `export PATH="` + shown + `:$PATH"`
	default: // zsh is the default on macOS and common on Linux
		return filepath.Join(in.Home, ".zshrc"), `export PATH="` + shown + `:$PATH"`
	}
}

// AddToShellRC appends line to file unless it is already there.
func AddToShellRC(file, line string) (bool, error) {
	b, err := os.ReadFile(file)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if strings.Contains(string(b), line) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return false, err
	}
	f, err := os.OpenFile(file, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return false, err
	}
	defer f.Close()
	prefix := "\n"
	if len(b) == 0 || strings.HasSuffix(string(b), "\n") {
		prefix = ""
	}
	_, err = fmt.Fprintf(f, "%s\n# deploy-dev\n%s\n", prefix, line)
	return err == nil, err
}

func installCmd(env *Env) *cobra.Command {
	var dir string
	var addPath, noPath bool
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Instala o deploy-dev no PATH do usuário (sem admin)",
		Long: `Copia este binário para uma pasta de programas do usuário e garante que
ela está no PATH:

  Linux/macOS  ~/.local/bin/deploy-dev
               (se não estiver no PATH, adiciona a linha no ~/.zshrc,
               ~/.bashrc ou config.fish, conforme o seu shell)
  Windows      %LOCALAPPDATA%\Programs\deploy-dev\deploy-dev.exe
               (adicionada ao PATH do usuário)

Rodar de novo com uma versão mais nova atualiza a instalação.`,
		Example: `  ./deploy-dev-linux-amd64 install
  deploy-dev-windows-amd64.exe install
  ./deploy-dev-darwin-arm64 install --dir /usr/local/bin   # pasta própria (pode exigir sudo)`,
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error { return runInstall(env, dir, addPath, noPath) },
	}
	cmd.Flags().StringVar(&dir, "dir", "", "pasta de instalação (padrão: a do usuário, ver acima)")
	cmd.Flags().BoolVar(&addPath, "add-to-path", false, "adiciona ao PATH sem perguntar")
	cmd.Flags().BoolVar(&noPath, "no-path", false, "não mexe no PATH")
	cmd.MarkFlagsMutuallyExclusive("add-to-path", "no-path")
	return cmd
}

func runInstall(env *Env, dir string, addPath, noPath bool) error {
	in, err := newInstaller()
	if err != nil {
		return err
	}
	if dir == "" {
		dir = in.DefaultDir()
	}
	dst, already, err := in.Copy(dir)
	if err != nil {
		return fmt.Errorf("instalando em %s: %w", dir, err)
	}
	if already {
		env.UI.OK("Já é este binário: %s", dst)
	} else {
		env.UI.OK("deploy-dev %s instalado em %s", env.Version, dst)
	}
	if in.GOOS == "darwin" {
		clearQuarantine(dst)
	}
	if in.OnPath(dir) {
		env.UI.Println("Pronto: rode `deploy-dev` em qualquer terminal.")
		return nil
	}
	if noPath {
		env.UI.Warn("%s não está no PATH (pulado por --no-path).", dir)
		return nil
	}
	return ensureOnPath(env, in, dir, addPath)
}

// installed reports whether this very binary is the one installed in the
// default per-user directory.
func installed() bool {
	in, err := newInstaller()
	if err != nil {
		return true // nothing useful to offer
	}
	dst := filepath.Join(in.DefaultDir(), in.binName())
	return cleanReal(in.Self) == cleanReal(dst) && in.OnPath(in.DefaultDir())
}

func ensureOnPath(env *Env, in *Installer, dir string, yes bool) error {
	if in.GOOS == "windows" {
		if !yes && env.UI.Interactive {
			ok, err := env.UI.ConfirmDefault("Adicionar "+dir+" ao PATH do usuário?", "", true)
			if err != nil {
				return err
			}
			if !ok {
				env.UI.Warn("Sem PATH: chame pelo caminho completo ou adicione %s manualmente.", dir)
				return nil
			}
		}
		added, err := addToUserPath(dir)
		if err != nil {
			return fmt.Errorf("adicionando ao PATH: %w", err)
		}
		if added {
			env.UI.OK("%s adicionado ao PATH do usuário.", dir)
		}
		env.UI.Println("Abra um terminal novo para o PATH valer.")
		return nil
	}

	file, line := in.ShellRC(dir)
	if !yes {
		if !env.UI.Interactive {
			env.UI.Warn("%s não está no PATH. Adicione ao %s:\n    %s\n  (ou rode com --add-to-path)", dir, file, line)
			return nil
		}
		ok, err := env.UI.ConfirmDefault(dir+" não está no PATH. Adicionar ao "+file+"?", line, true)
		if err != nil {
			return err
		}
		if !ok {
			env.UI.Warn("Adicione manualmente ao %s:\n    %s", file, line)
			return nil
		}
	}
	added, err := AddToShellRC(file, line)
	if err != nil {
		return err
	}
	if added {
		env.UI.OK("Adicionado ao %s.", file)
	}
	env.UI.Println("Abra um terminal novo (ou rode `source " + file + "`) para o PATH valer.")
	return nil
}
