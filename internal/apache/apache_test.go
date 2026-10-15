package apache

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vinicgobbi/deploy-tools/internal/run"
)

var update = flag.Bool("update", false, "rewrite golden files")

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("%s mismatch (run with -update):\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

func TestRenderGolden(t *testing.T) {
	for _, typ := range []string{"laravel", "symfony", "react", "angular", "static"} {
		doc := "/var/www/app/current"
		dir := "/var/www/app"
		if typ == "laravel" || typ == "symfony" {
			doc += "/public"
			dir = doc
		}
		got, err := Render(VhostData{
			App: "app", Type: typ, Domains: []string{"app.example.com", "www.app.example.com"}, DocRoot: doc, DirRoot: dir,
			CertFile: "/c.crt", CertKey: "/c.key", CertChain: "/c.chain",
		})
		if err != nil {
			t.Fatal(err)
		}
		golden(t, "vhost-"+typ+".conf", got)
	}
}

const legacyVhost = `<VirtualHost *:443>
    ServerName api.example.com
    DocumentRoot "/var/www/api/public/"
</VirtualHost>

<Directory /var/www/api/public>
   Options Indexes
   AllowOverride All
</Directory>

<Directory /var/www/other>
   Options -Indexes
</Directory>
`

func TestRepoint(t *testing.T) {
	got, n := Repoint(legacyVhost, "/var/www/api/public", "/var/www/api/current/public")
	if n != 1 {
		t.Fatalf("changed %d DocumentRoot lines, want 1", n)
	}
	golden(t, "repoint-laravel.conf", got)

	// No Options line in the block: one is added.
	got, _ = Repoint("<Directory /var/www/docs/>\n  Require all granted\n</Directory>\n", "/var/www/docs", "/var/www/docs/current")
	if !strings.Contains(got, "<Directory /var/www/docs/current/>\n  Require all granted\n    Options +FollowSymLinks\n</Directory>") {
		t.Fatalf("got:\n%s", got)
	}
}

func TestWithFollowSymLinks(t *testing.T) {
	cases := map[string]string{
		"-Indexes":                 "-Indexes +FollowSymLinks",
		"-Indexes -FollowSymLinks": "-Indexes +FollowSymLinks",
		"Indexes":                  "Indexes FollowSymLinks",
		"All":                      "All",
		"None":                     "None FollowSymLinks",
		"+FollowSymLinks -Indexes": "+FollowSymLinks -Indexes",
		"Indexes FollowSymLinks":   "Indexes FollowSymLinks",
	}
	for in, want := range cases {
		if got := withFollowSymLinks(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

func TestFindVhosts(t *testing.T) {
	root := t.TempDir()
	avail, enabled := filepath.Join(root, "avail"), filepath.Join(root, "enabled")
	_ = os.MkdirAll(avail, 0o755)
	_ = os.MkdirAll(enabled, 0o755)
	_ = os.WriteFile(filepath.Join(avail, "api.conf"), []byte(legacyVhost), 0o644)
	_ = os.WriteFile(filepath.Join(avail, "off.conf"), []byte("DocumentRoot /var/www/api/public\n"), 0o644)
	_ = os.Symlink("../avail/api.conf", filepath.Join(enabled, "api.conf"))

	got, err := FindVhosts(enabled, "/var/www/api", "/var/www/api/public")
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(filepath.Join(avail, "api.conf"))
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %v, want [%s] (disabled vhosts must be ignored)", got, want)
	}
}

func TestApplyRollsBackOnConfigtestFailure(t *testing.T) {
	f := &run.Fake{Fail: map[string]error{"apache2ctl configtest": run.ErrFake}}
	rolled := false
	if err := Apply(f, func() { rolled = true }); err == nil {
		t.Fatal("expected error")
	}
	if !rolled {
		t.Fatal("rollback not called")
	}
	for _, c := range f.Strings() {
		if strings.HasPrefix(c, "systemctl") {
			t.Fatal("reloaded despite failed configtest")
		}
	}
}

func TestServerName(t *testing.T) {
	if got := ServerName("<VirtualHost *:443>\n    servername app.example.com\n    ServerAlias x\n"); got != "app.example.com" {
		t.Fatalf("got %q", got)
	}
	if got := ServerName("DocumentRoot /x\n"); got != "" {
		t.Fatalf("got %q", got)
	}
}

const handEdited = `<VirtualHost *:80>
    ServerName old.example.com
    ServerAlias www.old.example.com
    Redirect permanent / https://old.example.com/
</VirtualHost>

<VirtualHost *:443>
    ServerName old.example.com
    ServerAlias www.old.example.com
    ServerAlias extra.example.com
    DocumentRoot /var/www/x/current
    Header set X-Custom yes
    SSLEngine on
    SSLCertificateFile /old.crt
    SSLCertificateChainFile /old.chain
    SSLCertificateKeyFile /old.key
</VirtualHost>
`

func TestDomains(t *testing.T) {
	got := Domains(handEdited)
	if strings.Join(got, " ") != "old.example.com www.old.example.com extra.example.com" {
		t.Fatalf("Domains = %v", got)
	}
}

func TestSetDomains(t *testing.T) {
	got, err := SetDomains(handEdited, []string{"new.example.com", "api.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"    ServerName new.example.com\n    ServerAlias api.example.com\n    Redirect permanent / https://new.example.com/\n",
		"    ServerName new.example.com\n    ServerAlias api.example.com\n    DocumentRoot /var/www/x/current\n    Header set X-Custom yes\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "old.example.com") {
		t.Fatalf("old domain left behind:\n%s", got)
	}
	got, _ = SetDomains(got, []string{"solo.example.com"})
	if strings.Contains(got, "ServerAlias") || Domains(got)[0] != "solo.example.com" {
		t.Fatalf("single domain:\n%s", got)
	}
	if _, err := SetDomains("Listen 80\n", []string{"a.example.com"}); err == nil {
		t.Fatal("expected error without <VirtualHost>")
	}
}

func TestSetCert(t *testing.T) {
	got, n := SetCert(handEdited, "/le/fullchain.pem", "/le/privkey.pem", "")
	if n != 1 || strings.Contains(got, "Chain") || !strings.Contains(got,
		"    SSLCertificateFile /le/fullchain.pem\n    SSLCertificateKeyFile /le/privkey.pem\n") {
		t.Fatalf("SetCert (%d):\n%s", n, got)
	}
	got, _ = SetCert(got, "/c.crt", "/c.key", "/c.bundle")
	if f, k, c := CertFiles(got); f != "/c.crt" || k != "/c.key" || c != "/c.bundle" {
		t.Fatalf("CertFiles = %q %q %q", f, k, c)
	}
	if _, n := SetCert("<VirtualHost *:80>\n</VirtualHost>\n", "/a", "/b", ""); n != 0 {
		t.Fatal("no SSL block should change nothing")
	}
}

func TestRenderHTTPOnlyAndEnableTLS(t *testing.T) {
	d := VhostData{
		App: "app", Type: "react", Domains: []string{"app.example.com", "www.app.example.com"},
		DocRoot: "/var/www/app/current", DirRoot: "/var/www/app",
	}
	http, err := Render(d)
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "vhost-react-http.conf", http)
	if HasTLS(http) || strings.Contains(http, ":443") {
		t.Fatalf("HTTP-only vhost has TLS:\n%s", http)
	}

	// A hand edit inside the :80 block must survive the conversion.
	edited := strings.Replace(http, "    DocumentRoot", "    Header set X-Custom yes\n    DocumentRoot", 1)
	got, err := EnableTLS(edited, "/le/fullchain.pem", "/le/privkey.pem", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{
		"<VirtualHost *:80>\n    ServerName app.example.com\n    ServerAlias www.app.example.com\n    RewriteEngine On\n",
		"<VirtualHost *:443>\n    ServerName app.example.com\n    ServerAlias www.app.example.com\n\n    Header set X-Custom yes\n",
		"    SSLEngine on\n    SSLCertificateFile /le/fullchain.pem\n    SSLCertificateKeyFile /le/privkey.pem\n</VirtualHost>",
		"<Directory /var/www/app/>",
	} {
		if !strings.Contains(got, part) {
			t.Fatalf("missing %q in:\n%s", part, got)
		}
	}
	if f, k, _ := CertFiles(got); f != "/le/fullchain.pem" || k != "/le/privkey.pem" || !HasTLS(got) {
		t.Fatalf("cert = %q %q", f, k)
	}
	if _, err := EnableTLS(got, "/a", "/b", ""); err == nil {
		t.Fatal("EnableTLS twice must fail")
	}
}
