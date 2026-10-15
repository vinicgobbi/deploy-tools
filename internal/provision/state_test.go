package provision

import (
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestInstallState(t *testing.T) {
	p, f := newP(t)
	loose := filepath.Join(t.TempDir(), "deploy-tools-linux-amd64")
	write(t, loose, "new")

	if st := p.InstallState(loose, "v0.2.0"); st.Installed || st.Running {
		t.Fatalf("fresh server: %+v", st)
	}
	if err := p.Install(loose); err != nil {
		t.Fatal(err)
	}
	if st := p.InstallState(p.Paths.BinaryPath(), "v0.2.0"); !st.Running || !st.Wrapper || st.InstalledVersion != "v0.2.0" {
		t.Fatalf("installed, running it: %+v", st)
	}
	f.Output = map[string]string{p.Paths.BinaryPath() + " version": "deploy-tools v0.1.0\ninstalado em ...\n"}
	if st := p.InstallState(loose, "v0.2.0"); !st.Installed || st.Running || st.InstalledVersion != "v0.1.0" {
		t.Fatalf("loose copy: %+v", st)
	}
}

func TestSecrets(t *testing.T) {
	p, _ := newP(t)
	write(t, filepath.Join(p.Paths.SSHDir, "ssh_host_rsa_key.pub"), "ssh-rsa AAAArsa root@srv\n")
	write(t, filepath.Join(p.Paths.SSHDir, "ssh_host_ed25519_key.pub"), "ssh-ed25519 AAAAed root@srv\n")
	write(t, filepath.Join(p.Paths.SSHDir, "ssh_host_ed25519_key"), "PRIVATE — must never be read\n")
	pub := filepath.Join(t.TempDir(), "ci.pub")
	write(t, pub, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl ci-deploy\n")
	write(t, p.Paths.AuthorizedKeys(), "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAdmin admin\n")
	if err := p.AddKey(pub); err != nil {
		t.Fatal(err)
	}

	s := p.Secrets("deploy.example.com")
	if s.Host != "deploy.example.com" || s.User != "deploy" {
		t.Fatalf("secrets = %+v", s)
	}
	if got := strings.Join(s.HostKeys, "\n"); got != "deploy.example.com ssh-ed25519 AAAAed\ndeploy.example.com ssh-rsa AAAArsa" {
		t.Fatalf("host keys:\n%s", got)
	}
	if len(s.CIKeys) != 1 || s.CIKeys[0].Comment != "ci-deploy" {
		t.Fatalf("CI keys = %+v (the admin key has no command= and must not be listed)", s.CIKeys)
	}

	p.Paths.DeployHost = "1.2.3.4"
	if s := p.Secrets(""); s.Host != "1.2.3.4" || !strings.HasPrefix(s.HostKeys[0], "1.2.3.4 ") {
		t.Fatalf("config host: %+v", s)
	}
	_ = os.Remove(p.Paths.AuthorizedKeys())
	if s := p.Secrets(""); len(s.CIKeys) != 0 {
		t.Fatalf("no keys: %+v", s.CIKeys)
	}
}

func TestGenerateKey(t *testing.T) {
	p, _ := newP(t)
	priv, key, err := p.GenerateKey("ci-deploy@github")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(priv), "-----BEGIN OPENSSH PRIVATE KEY-----") {
		t.Fatalf("private key:\n%s", priv)
	}
	signer, err := ssh.ParsePrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	keys, _ := p.Keys()
	if len(keys) != 1 || keys[0].Blob != key.Blob || keys[0].Command != p.Paths.WrapperPath() || keys[0].Comment != "ci-deploy@github" {
		t.Fatalf("authorized = %+v", keys)
	}
	if got := base64.StdEncoding.EncodeToString(signer.PublicKey().Marshal()); got != keys[0].Blob {
		t.Fatal("authorized key is not the public half of the returned private key")
	}
	if b, _ := os.ReadFile(p.Paths.AuthorizedKeys()); strings.Contains(string(b), "PRIVATE") {
		t.Fatal("private key written to authorized_keys")
	}
	// ssh-keygen, when present, must read the key too.
	if path, err := exec.LookPath("ssh-keygen"); err == nil {
		f := filepath.Join(t.TempDir(), "k")
		_ = os.WriteFile(f, priv, 0o600)
		out, err := exec.Command(path, "-y", "-f", f).Output()
		if err != nil || !strings.Contains(string(out), key.Blob) {
			t.Fatalf("ssh-keygen -y: %v %s", err, out)
		}
	}
}
