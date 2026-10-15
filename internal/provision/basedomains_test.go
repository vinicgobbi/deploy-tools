package provision

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/vinicgobbi/deploy-tools/internal/apache"
	"github.com/vinicgobbi/deploy-tools/internal/config"
	"github.com/vinicgobbi/deploy-tools/internal/tlscert/tlstest"
)

func TestBaseDomainWithRegisteredCert(t *testing.T) {
	p, _ := newP(t)
	p.Paths.BaseDomains = nil
	// Outside every searched directory: only usable because it is registered.
	elsewhere := t.TempDir()
	full := tlstest.LetsEncrypt(t, elsewhere, "wild", []string{"vinicgobbi.dev.br", "*.vinicgobbi.dev.br"}, future)
	key := filepath.Join(filepath.Dir(full), "privkey.pem")
	narrow := tlstest.LetsEncrypt(t, elsewhere, "narrow", []string{"x.vinicgobbi.dev.br"}, future)

	if _, err := p.AddBaseDomain("vinicgobbi.dev.br", config.CertFiles{File: narrow, Key: key}); err == nil {
		t.Fatal("key of another certificate accepted")
	}
	if _, err := p.AddBaseDomain("vinicgobbi.dev.br", config.CertFiles{File: narrow, Key: filepath.Join(filepath.Dir(narrow), "privkey.pem")}); err == nil ||
		!strings.Contains(err.Error(), "não é coringa") {
		t.Fatalf("non-wildcard accepted: %v", err)
	}
	if ok, err := p.AddBaseDomain("vinicgobbi.dev.br", config.CertFiles{File: full, Key: key}); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if loaded, _ := config.Load(p.Paths.ConfigFile); loaded.BaseCerts["vinicgobbi.dev.br"].File != full {
		t.Fatalf("not saved: %+v", loaded.BaseCerts)
	}
	if err := p.Create(CreateOptions{App: "api", Type: "static"}); err != nil {
		t.Fatal(err)
	}
	vhost := read(t, p.Paths.VhostFile("api"))
	if f, k, _ := apache.CertFiles(vhost); f != full || k != key || !apache.HasTLS(vhost) {
		t.Fatalf("cert = %q %q", f, k)
	}
	if err := p.RemoveBaseDomain("vinicgobbi.dev.br"); err != nil {
		t.Fatal(err)
	}
	if loaded, _ := config.Load(p.Paths.ConfigFile); len(loaded.BaseCerts) != 0 {
		t.Fatalf("cert left behind: %+v", loaded.BaseCerts)
	}
}

func TestCreateWithBaseDomains(t *testing.T) {
	p, _ := newP(t)
	p.Paths.BaseDomains = []string{"example.com", "vinicgobbi.dev.br"}

	err := p.Create(CreateOptions{App: "api", Type: "static"})
	if err == nil || !strings.Contains(err.Error(), "--base example.com") {
		t.Fatalf("several bases without a choice: %v", err)
	}
	if err := p.Create(CreateOptions{App: "api", Type: "static", Base: "nope.com"}); err == nil ||
		!strings.Contains(err.Error(), "não está cadastrado") {
		t.Fatalf("unregistered base: %v", err)
	}
	full := tlstest.LetsEncrypt(t, p.Paths.CertDirs[0], "vinicgobbi.dev.br", []string{"vinicgobbi.dev.br", "*.vinicgobbi.dev.br"}, future)
	if err := p.Create(CreateOptions{App: "minha_api", Type: "static", Base: "*.Vinicgobbi.dev.br"}); err != nil {
		t.Fatal(err)
	}
	vhost := read(t, p.Paths.VhostFile("minha_api"))
	if got := apache.Domains(vhost); len(got) != 1 || got[0] != "minha-api.vinicgobbi.dev.br" {
		t.Fatalf("domains = %v", got)
	}
	if f, _, _ := apache.CertFiles(vhost); f != full {
		t.Fatalf("wildcard cert not used: %q", f)
	}
	if err := p.Create(CreateOptions{App: "front", Type: "react", Base: "vinicgobbi.dev.br", Sub: "app"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := p.Domains("front"); got[0] != "app.vinicgobbi.dev.br" {
		t.Fatalf("sub: %v", got)
	}
}

func TestAddRemoveBaseDomain(t *testing.T) {
	p, _ := newP(t)
	p.Paths.BaseDomains = nil
	if ok, err := p.AddBaseDomain("*.vinicgobbi.dev.br", config.CertFiles{}); err != nil || ok {
		t.Fatal(ok, err)
	}
	if !strings.Contains(p.Out.(interface{ String() string }).String(), "Nenhum certificado *.vinicgobbi.dev.br") {
		t.Fatalf("out: %s", p.Out)
	}
	tlstest.LetsEncrypt(t, p.Paths.CertDirs[0], "wild", []string{"*.example.org"}, future)
	if ok, err := p.AddBaseDomain("example.org", config.CertFiles{}); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if !strings.Contains(p.Out.(interface{ String() string }).String(), "Certificado coringa encontrado") {
		t.Fatalf("out: %s", p.Out)
	}
	loaded, err := config.Load(p.Paths.ConfigFile)
	if err != nil || strings.Join(loaded.BaseDomains, " ") != "vinicgobbi.dev.br example.org" {
		t.Fatalf("config: %v %v", loaded.BaseDomains, err)
	}
	if _, err := p.AddBaseDomain("bad domain", config.CertFiles{}); err == nil {
		t.Fatal("invalid domain accepted")
	}
	if err := p.RemoveBaseDomain("vinicgobbi.dev.br"); err != nil {
		t.Fatal(err)
	}
	if err := p.RemoveBaseDomain("vinicgobbi.dev.br"); err == nil {
		t.Fatal("removing twice must fail")
	}
	if loaded, _ := config.Load(p.Paths.ConfigFile); strings.Join(loaded.BaseDomains, " ") != "example.org" {
		t.Fatalf("after remove: %v", loaded.BaseDomains)
	}
}
