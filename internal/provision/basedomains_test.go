package provision

import (
	"strings"
	"testing"

	"github.com/vinicgobbi/deploy-tools/internal/apache"
	"github.com/vinicgobbi/deploy-tools/internal/config"
	"github.com/vinicgobbi/deploy-tools/internal/tlscert/tlstest"
)

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
	if err := p.AddBaseDomain("*.vinicgobbi.dev.br"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Out.(interface{ String() string }).String(), "nenhum certificado *.vinicgobbi.dev.br") {
		t.Fatalf("out: %s", p.Out)
	}
	tlstest.LetsEncrypt(t, p.Paths.CertDirs[0], "wild", []string{"*.example.org"}, future)
	if err := p.AddBaseDomain("example.org"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Out.(interface{ String() string }).String(), "Certificado coringa encontrado") {
		t.Fatalf("out: %s", p.Out)
	}
	loaded, err := config.Load(p.Paths.ConfigFile)
	if err != nil || strings.Join(loaded.BaseDomains, " ") != "vinicgobbi.dev.br example.org" {
		t.Fatalf("config: %v %v", loaded.BaseDomains, err)
	}
	if err := p.AddBaseDomain("bad domain"); err == nil {
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
