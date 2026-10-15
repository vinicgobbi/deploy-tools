package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMissingFile(t *testing.T) {
	p, err := Load(filepath.Join(t.TempDir(), "nope.conf"))
	if err != nil || p.CertFile != "" || len(p.BaseDomains) != 0 || len(p.CertDirs) != len(Default().CertDirs) {
		t.Fatalf("Load = %+v, %v", p, err)
	}
}

func TestLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deploy-tools.conf")
	body := `# servidor
DOMAIN_SUFFIX=example.com
BASE_DOMAINS=*.vinicgobbi.dev.br, Example.com
SSL_CERT_FILE="/etc/letsencrypt/live/example.com/fullchain.pem"
SSL_CERT_KEY = /etc/letsencrypt/live/example.com/privkey.pem
SSL_SEARCH_DIRS=/srv/certs:/opt/tls
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(p.BaseDomains, " ") != "example.com vinicgobbi.dev.br" || p.CertFile != "/etc/letsencrypt/live/example.com/fullchain.pem" ||
		p.CertKey != "/etc/letsencrypt/live/example.com/privkey.pem" || p.CertChain != "" ||
		p.CertDirs[0] != "/srv/certs" || p.CertDirs[1] != "/opt/tls" || p.CertDirs[2] != "/etc/letsencrypt/live" {
		t.Fatalf("Load = %+v", p)
	}
}

func TestLoadRejectsUnknownKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deploy-tools.conf")
	if err := os.WriteFile(path, []byte("APPS_ROOT=/tmp\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for unknown key")
	}
}

func TestSetBaseDomains(t *testing.T) {
	path := filepath.Join(t.TempDir(), "etc", "deploy-tools.conf")
	if err := SetBaseDomains(path, []string{"a.example.com"}, nil); err != nil {
		t.Fatal(err)
	}
	if p, err := Load(path); err != nil || strings.Join(p.BaseDomains, " ") != "a.example.com" {
		t.Fatalf("created: %+v %v", p.BaseDomains, err)
	}

	body := "# meu servidor\nDEPLOY_HOST=srv\nDOMAIN_SUFFIX=.old.com\n\n# cert\nSSL_CERT_FILE=/c.pem\n"
	if err := os.WriteFile(path, []byte(body), 0o640); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(path, 0o640)
	certs := map[string]CertFiles{"vinicgobbi.dev.br": {File: "/le/fullchain.pem", Key: "/le/privkey.pem"}, "gone.com": {File: "/x", Key: "/y"}}
	if err := SetBaseDomains(path, []string{"old.com", "vinicgobbi.dev.br"}, certs); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	want := "# meu servidor\nDEPLOY_HOST=srv\nBASE_DOMAINS=old.com,vinicgobbi.dev.br\n" +
		"BASE_CERT=vinicgobbi.dev.br /le/fullchain.pem /le/privkey.pem\n\n# cert\nSSL_CERT_FILE=/c.pem\n"
	if string(b) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", b, want)
	}
	if p, err := Load(path); err != nil || p.BaseCerts["vinicgobbi.dev.br"].Key != "/le/privkey.pem" || len(p.BaseCerts) != 1 {
		t.Fatalf("reload: %+v %v", p.BaseCerts, err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v", fi.Mode().Perm())
	}
}

func TestSetDeployHost(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deploy-tools.conf")
	if err := SetBaseDomains(path, []string{"example.com"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := SetDeployHost(path, "179.199.156.62"); err != nil {
		t.Fatal(err)
	}
	if err := SetDeployHost(path, "deploy.example.com"); err != nil {
		t.Fatal(err)
	}
	p, err := Load(path)
	if err != nil || p.DeployHost != "deploy.example.com" || p.BaseDomains[0] != "example.com" {
		t.Fatalf("Load = %+v %v", p, err)
	}
	if b, _ := os.ReadFile(path); strings.Count(string(b), "DEPLOY_HOST=") != 1 {
		t.Fatalf("duplicated:\n%s", b)
	}
}
