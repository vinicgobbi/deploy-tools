package provision

import (
	"bufio"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/vinicgobbi/deploy-tools/internal/run"
)

// Install puts this binary at /usr/local/bin/deploy-tools and makes
// /usr/local/bin/deploy-ci (the path in every authorized_keys command=) a
// symlink to it. Both swaps are rename()s, so a deploy running at that
// moment sees either the old or the new program, never half a file. A
// deploy-ci that was a regular file (a hand-made wrapper) is kept as
// deploy-ci.bak.
func (p *Provisioner) Install(self string) error {
	if err := os.MkdirAll(p.Paths.BinDir, 0o755); err != nil {
		return err
	}
	dst := p.Paths.BinaryPath()
	selfReal, _ := filepath.EvalSymlinks(self)
	dstReal, _ := filepath.EvalSymlinks(dst)
	if selfReal == "" || selfReal != dstReal {
		if err := copyFile(self, dst+".new", 0o755); err != nil {
			return err
		}
		if err := os.Rename(dst+".new", dst); err != nil {
			return err
		}
		p.say("Binário instalado em %s", dst)
	} else {
		p.say("Binário já está em %s", dst)
	}

	wrapper := p.Paths.WrapperPath()
	if fi, err := os.Lstat(wrapper); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			if t, _ := os.Readlink(wrapper); t == filepath.Base(dst) || t == dst {
				p.say("%s já aponta para o binário.", wrapper)
				return nil
			}
		} else if bak := wrapper + ".bak"; !exists(bak) {
			if err := copyFile(wrapper, bak, 0o644); err != nil {
				return err
			}
			p.say("%s anterior guardado em %s", filepath.Base(wrapper), bak)
		}
	}
	tmp := wrapper + ".tmp"
	_ = os.Remove(tmp)
	if err := os.Symlink(filepath.Base(dst), tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, wrapper); err != nil {
		return err
	}
	p.say("%s -> %s", wrapper, filepath.Base(dst))
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
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
	return os.Chmod(dst, mode)
}

// Setup bootstraps a fresh server: deploy user, ~/.ssh, binary, and
// optionally the CI key. Idempotent.
func (p *Provisioner) Setup(self, pubKey string) error {
	u := p.Paths.DeployUser
	if _, err := user.Lookup(u); err != nil {
		// /bin/sh, not nologin: sshd runs the forced command via the login
		// shell ($SHELL -c ...), and nologin ignores -c, which silently
		// breaks the forced command. Access is restricted by command= +
		// no-pty in authorized_keys, not by the shell.
		if _, err := run.Output(p.Runner, run.Cmd{Name: "useradd", Args: []string{
			"--system", "--create-home", "--home-dir", p.Paths.DeployHome,
			"--shell", "/bin/sh", "--groups", p.Paths.WebGroup, u,
		}}); err != nil {
			return err
		}
		p.say("Usuário %s criado.", u)
	} else {
		if _, err := run.Output(p.Runner, run.Cmd{Name: "usermod", Args: []string{"-aG", p.Paths.WebGroup, u}}); err != nil {
			return err
		}
		out, _ := run.Output(p.Runner, run.Cmd{Name: "getent", Args: []string{"passwd", u}})
		if strings.HasSuffix(strings.TrimSpace(out), "nologin") {
			if _, err := run.Output(p.Runner, run.Cmd{Name: "usermod", Args: []string{"--shell", "/bin/sh", u}}); err != nil {
				return err
			}
			p.say("Shell de %s corrigido: nologin -> /bin/sh (nologin quebra o command= forçado).", u)
		}
		p.say("Usuário %s já existe.", u)
	}

	sshDir := filepath.Dir(p.Paths.AuthorizedKeys())
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(sshDir, 0o700); err != nil {
		return err
	}
	if err := p.Owner.Chown(sshDir, u, u, false); err != nil {
		return err
	}
	if err := p.Install(self); err != nil {
		return err
	}
	if pubKey != "" {
		return p.AddKey(pubKey)
	}
	return nil
}

var keyTypeRe = regexp.MustCompile(`^(ssh-(ed25519|rsa|dss)|ecdsa-sha2-nistp\d+|sk-[a-z0-9@.-]+)$`)

type Key struct {
	Type, Blob, Comment string
	Command             string // forced command, "" if none
}

func (k Key) Fingerprint() string {
	raw, err := base64.StdEncoding.DecodeString(k.Blob)
	if err != nil {
		return "?"
	}
	sum := sha256.Sum256(raw)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

var commandOptRe = regexp.MustCompile(`command="((?:[^"\\]|\\.)*)"`)

// parseKeyLine understands "[options] type blob [comment]".
func parseKeyLine(line string) (Key, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return Key{}, false
	}
	fields := strings.Fields(line)
	for i := 0; i+1 < len(fields); i++ {
		if keyTypeRe.MatchString(fields[i]) {
			k := Key{Type: fields[i], Blob: fields[i+1], Comment: strings.Join(fields[i+2:], " ")}
			if m := commandOptRe.FindStringSubmatch(strings.Join(fields[:i], " ")); m != nil {
				k.Command = m[1]
			}
			return k, true
		}
	}
	return Key{}, false
}

func (p *Provisioner) Keys() ([]Key, error) {
	f, err := os.Open(p.Paths.AuthorizedKeys())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var keys []Key
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		if k, ok := parseKeyLine(sc.Text()); ok {
			keys = append(keys, k)
		}
	}
	return keys, sc.Err()
}

// AddKey appends a public key restricted to the forced command. A key that
// is already present (same blob) is left alone.
func (p *Provisioner) AddKey(pubKeyFile string) error {
	b, err := os.ReadFile(pubKeyFile)
	if err != nil {
		return err
	}
	k, ok := parseKeyLine(strings.SplitN(strings.TrimSpace(string(b)), "\n", 2)[0])
	if !ok {
		return fmt.Errorf("%s não parece uma chave pública SSH (esperado: ssh-ed25519 AAAA... comentário)", pubKeyFile)
	}
	if strings.HasPrefix(strings.TrimSpace(string(b)), "-----BEGIN") {
		return fmt.Errorf("%s é uma chave PRIVADA — use o arquivo .pub", pubKeyFile)
	}
	existing, err := p.Keys()
	if err != nil {
		return err
	}
	for _, e := range existing {
		if e.Blob == k.Blob {
			p.say("Chave %s já está no authorized_keys.", k.Fingerprint())
			return nil
		}
	}

	path := p.Paths.AuthorizedKeys()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	// Make sure we start on a fresh line.
	if fi, _ := f.Stat(); fi.Size() > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, fi.Size()-1); err == nil && last[0] != '\n' {
			_, _ = f.WriteString("\n")
		}
	}
	line := fmt.Sprintf(`command="%s",no-port-forwarding,no-X11-forwarding,no-agent-forwarding,no-pty %s %s`,
		p.Paths.WrapperPath(), k.Type, k.Blob)
	if k.Comment != "" {
		line += " " + k.Comment
	}
	if _, err := f.WriteString(line + "\n"); err != nil {
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	if err := p.Owner.Chown(path, p.Paths.DeployUser, p.Paths.DeployUser, false); err != nil {
		return err
	}
	p.say("Chave %s adicionada (restrita a %s).", k.Fingerprint(), p.Paths.WrapperPath())
	return nil
}
