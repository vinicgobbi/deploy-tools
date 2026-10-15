package provision

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vinicgobbi/deploy-tools/internal/apache"
	"github.com/vinicgobbi/deploy-tools/internal/run"
	"github.com/vinicgobbi/deploy-tools/internal/tlscert/tlstest"
)

var future = time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)

// enable links the site like a2ensite would (the fake runner doesn't).
func enable(t *testing.T, p *Provisioner, app string) {
	t.Helper()
	if err := os.Symlink(p.Paths.VhostFile(app), filepath.Join(p.Paths.SitesEnabled, app+".conf")); err != nil {
		t.Fatal(err)
	}
}

func TestCreateWithDomainsFindsCertificate(t *testing.T) {
	p, _ := newP(t)
	live := p.Paths.CertDirs[0]
	full := tlstest.LetsEncrypt(t, live, "shop.example.org", []string{"shop.example.org", "www.shop.example.org"}, future)

	err := p.Create(CreateOptions{App: "shop", Type: "static", Domains: []string{"Shop.example.org", "www.shop.example.org,shop.example.org"}})
	if err != nil {
		t.Fatal(err)
	}
	vhost := read(t, p.Paths.VhostFile("shop"))
	if got := apache.Domains(vhost); strings.Join(got, " ") != "shop.example.org www.shop.example.org" {
		t.Fatalf("domains = %v", got)
	}
	if f, k, c := apache.CertFiles(vhost); f != full || k != filepath.Join(filepath.Dir(full), "privkey.pem") || c != "" {
		t.Fatalf("cert = %q %q %q", f, k, c)
	}
}

func TestCreateWithoutCertificateIsHTTPOnly(t *testing.T) {
	p, _ := newP(t)
	if err := p.Create(CreateOptions{App: "app", Type: "static"}); err != nil {
		t.Fatal(err)
	}
	vhost := read(t, p.Paths.VhostFile("app"))
	if f, _, _ := apache.CertFiles(vhost); f != "" || apache.HasTLS(vhost) || strings.Contains(vhost, ":443") {
		t.Fatalf("expected HTTP only:\n%s", vhost)
	}
	// Adding a domain once a certificate covers everything turns HTTPS on.
	tlstest.LetsEncrypt(t, p.Paths.CertDirs[0], "app", []string{"app.example.com", "www.app.example.com"}, future)
	if err := p.AddDomains("app", []string{"www.app.example.com"}); err != nil {
		t.Fatal(err)
	}
	if vhost := read(t, p.Paths.VhostFile("app")); !apache.HasTLS(vhost) || !strings.Contains(vhost, "<VirtualHost *:443>") {
		t.Fatalf("HTTPS not turned on:\n%s", vhost)
	}
}

func TestCreateRequiresDomainWithoutSuffix(t *testing.T) {
	p, _ := newP(t)
	p.Paths.BaseDomains = nil
	err := p.Create(CreateOptions{App: "app", Type: "static"})
	if err == nil || !strings.Contains(err.Error(), "domínio não informado") {
		t.Fatalf("err = %v", err)
	}
	if err := p.Create(CreateOptions{App: "app", Type: "static", Domains: []string{"bad domain"}}); err == nil {
		t.Fatal("expected invalid domain error")
	}
}

func TestAddRemoveDomainsSwapsCertificate(t *testing.T) {
	p, f := newP(t)
	live := p.Paths.CertDirs[0]
	tlstest.LetsEncrypt(t, live, "app.example.com", []string{"app.example.com"}, future)
	if err := p.Create(CreateOptions{App: "app", Type: "static"}); err != nil {
		t.Fatal(err)
	}
	enable(t, p, "app")
	// A wider certificate shows up later (e.g. certbot --expand).
	wide := tlstest.LetsEncrypt(t, live, "app.example.com-0001", []string{"app.example.com", "api.example.com"}, future.AddDate(0, 1, 0))
	f.Calls = nil

	if err := p.AddDomains("app", []string{"api.example.com"}); err != nil {
		t.Fatal(err)
	}
	vhost := read(t, p.Paths.VhostFile("app"))
	if got, _ := p.Domains("app"); strings.Join(got, " ") != "app.example.com api.example.com" {
		t.Fatalf("domains = %v", got)
	}
	if file, _, _ := apache.CertFiles(vhost); file != wide {
		t.Fatalf("cert = %q, want %q", file, wide)
	}
	if got := strings.Join(f.Strings(), "\n"); !strings.Contains(got, "apache2ctl configtest\napache2ctl configtest\nsystemctl reload apache2") {
		t.Fatalf("calls:\n%s", got)
	}

	if err := p.RemoveDomains("app", []string{"app.example.com"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := p.Domains("app"); strings.Join(got, " ") != "api.example.com" {
		t.Fatalf("domains = %v", got)
	}
	if err := p.RemoveDomains("app", []string{"api.example.com"}); err == nil {
		t.Fatal("removing the last domain must fail")
	}
	if err := p.RemoveDomains("app", []string{"nope.example.com"}); err == nil {
		t.Fatal("removing an unknown domain must fail")
	}
}

func TestSetDomainsRestoresOnConfigtestFailure(t *testing.T) {
	p, f := newP(t)
	if err := p.Create(CreateOptions{App: "app", Type: "static"}); err != nil {
		t.Fatal(err)
	}
	enable(t, p, "app")
	before := read(t, p.Paths.VhostFile("app"))
	tests := 0
	f.Func = func(c run.Cmd) error {
		if c.String() == "apache2ctl configtest" {
			if tests++; tests == 2 {
				return run.ErrFake
			}
		}
		return nil
	}
	err := p.AddDomains("app", []string{"other.example.com"})
	if err == nil || !errors.Is(err, run.ErrFake) {
		t.Fatalf("err = %v", err)
	}
	if after := read(t, p.Paths.VhostFile("app")); after != before {
		t.Fatalf("vhost not restored:\n%s", after)
	}
}

func TestUpdateCert(t *testing.T) {
	p, _ := newP(t)
	if err := p.Create(CreateOptions{App: "app", Type: "static", Domains: []string{"app.example.com", "www.app.example.com"}}); err != nil {
		t.Fatal(err)
	}
	// Still nothing on disk: stays HTTP only, no error (nothing to lose).
	if c, err := p.UpdateCert("app", CertChoice{}); err != nil || !c.Fallback {
		t.Fatal(c, err)
	}
	full := tlstest.LetsEncrypt(t, p.Paths.CertDirs[0], "app", []string{"app.example.com", "www.app.example.com"}, future)
	c, err := p.UpdateCert("app", CertChoice{})
	if err != nil || c.File != full || !apache.HasTLS(read(t, p.Paths.VhostFile("app"))) {
		t.Fatalf("UpdateCert = %+v, %v", c, err)
	}
	// With the real one gone, it refuses to downgrade to the fallback.
	p.Paths.CertDirs = nil
	if _, err := p.UpdateCert("app", CertChoice{}); err == nil {
		t.Fatal("expected refusal to swap a real certificate for the fallback")
	}
	// An explicit certificate wins and is checked for existence.
	if _, err := p.UpdateCert("app", CertChoice{File: "/nope.crt", Key: "/nope.key"}); err == nil {
		t.Fatal("expected missing file error")
	}
}
