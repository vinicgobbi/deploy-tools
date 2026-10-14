package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingFile(t *testing.T) {
	p, err := Load(filepath.Join(t.TempDir(), "nope.conf"))
	if err != nil || p.CertFile != "" || p.DomainSuffix != "" || len(p.CertDirs) != len(Default().CertDirs) {
		t.Fatalf("Load = %+v, %v", p, err)
	}
}

func TestLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deploy-tools.conf")
	body := `# servidor
DOMAIN_SUFFIX=example.com
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
	if p.DomainSuffix != ".example.com" || p.CertFile != "/etc/letsencrypt/live/example.com/fullchain.pem" ||
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
