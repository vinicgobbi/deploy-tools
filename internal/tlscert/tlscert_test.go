package tlscert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var now = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

type issued struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func issue(t *testing.T, parent *issued, ca bool, names []string, notAfter time.Time) *issued {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: cn(ca, names)},
		NotBefore:             now.Add(-24 * time.Hour),
		NotAfter:              notAfter,
		DNSNames:              names,
		IsCA:                  ca,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
	}
	signer, signerKey := tmpl, key
	if parent != nil {
		signer, signerKey = parent.cert, parent.key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, signer, &key.PublicKey, signerKey)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := x509.ParseCertificate(der)
	return &issued{c, key}
}

func cn(ca bool, names []string) string {
	if ca {
		return "Test CA"
	}
	if len(names) > 0 {
		return names[0]
	}
	return "leaf"
}

func writePEM(t *testing.T, path string, certs ...*issued) {
	t.Helper()
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	var b []byte
	for _, c := range certs {
		b = append(b, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.cert.Raw})...)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeKey(t *testing.T, path string, c *issued) {
	t.Helper()
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	der, _ := x509.MarshalPKCS8PrivateKey(c.key)
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestFindLetsEncrypt(t *testing.T) {
	root := t.TempDir()
	ca := issue(t, nil, true, nil, now.AddDate(5, 0, 0))
	le := issue(t, ca, false, []string{"example.com", "www.example.com"}, now.AddDate(0, 2, 0))
	writePEM(t, filepath.Join(root, "live/example.com/fullchain.pem"), le, ca)
	writeKey(t, filepath.Join(root, "live/example.com/privkey.pem"), le)

	c, ok := Find([]string{filepath.Join(root, "live")}, []string{"example.com", "www.example.com"}, now)
	if !ok || !c.LetsCrypt || c.Key != filepath.Join(root, "live/example.com/privkey.pem") || c.Chain != "" {
		t.Fatalf("Find = %+v, %v", c, ok)
	}
	if _, ok := Find([]string{filepath.Join(root, "live")}, []string{"api.example.com"}, now); ok {
		t.Fatal("certificate does not cover api.example.com")
	}
}

func TestFindLooseFilesWithWildcardKeyAndBundle(t *testing.T) {
	root := t.TempDir()
	certs, private := filepath.Join(root, "certs"), filepath.Join(root, "private")
	ca := issue(t, nil, true, nil, now.AddDate(5, 0, 0))
	wild := issue(t, ca, false, []string{"example.org", "*.example.org"}, now.AddDate(1, 0, 0))
	writePEM(t, filepath.Join(certs, "example_org.crt"), wild)
	writePEM(t, filepath.Join(certs, "example_org.ca-bundle"), ca)
	// Key with an unrelated name: matched by public key, not by file name.
	writeKey(t, filepath.Join(private, "chave-publica.key"), wild)
	// A key that matches nothing and an expired certificate for the same names.
	writeKey(t, filepath.Join(private, "outra.key"), issue(t, nil, false, nil, now))
	old := issue(t, ca, false, []string{"*.example.org"}, now.AddDate(0, 0, -1))
	writePEM(t, filepath.Join(certs, "old.crt"), old)
	writeKey(t, filepath.Join(private, "old.key"), old)

	c, ok := Find([]string{certs, private}, []string{"api.example.org", "example.org"}, now)
	if !ok {
		t.Fatal("not found")
	}
	if c.File != filepath.Join(certs, "example_org.crt") || c.Key != filepath.Join(private, "chave-publica.key") ||
		c.Chain != filepath.Join(certs, "example_org.ca-bundle") {
		t.Fatalf("Find = %+v", c)
	}
	if got := Missing(c, []string{"a.b.example.org", "example.org"}); len(got) != 1 || got[0] != "a.b.example.org" {
		t.Fatalf("Missing = %v", got)
	}
}

func TestScanPrefersCAIssuedOverSelfSigned(t *testing.T) {
	root := t.TempDir()
	self := issue(t, nil, false, []string{"example.net"}, now.AddDate(10, 0, 0))
	writePEM(t, filepath.Join(root, "self.pem"), self)
	writeKey(t, filepath.Join(root, "self.key"), self)
	ca := issue(t, nil, true, nil, now.AddDate(5, 0, 0))
	real := issue(t, ca, false, []string{"example.net"}, now.AddDate(0, 3, 0))
	writePEM(t, filepath.Join(root, "real.pem"), real, ca)
	writeKey(t, filepath.Join(root, "real.key"), real)

	c, ok := Find([]string{root}, []string{"example.net"}, now)
	if !ok || c.File != filepath.Join(root, "real.pem") || c.SelfSign {
		t.Fatalf("Find = %+v, %v", c, ok)
	}
}

func TestScanSkipsCertWithoutKey(t *testing.T) {
	root := t.TempDir()
	c := issue(t, nil, false, []string{"example.com"}, now.AddDate(1, 0, 0))
	writePEM(t, filepath.Join(root, "x.crt"), c)
	if got := Scan([]string{root, filepath.Join(root, "missing")}); len(got) != 0 {
		t.Fatalf("Scan = %+v", got)
	}
}

func TestCheckPair(t *testing.T) {
	root := t.TempDir()
	ca := issue(t, nil, true, nil, now.AddDate(5, 0, 0))
	leaf := issue(t, ca, false, []string{"*.example.com"}, now.AddDate(1, 0, 0))
	other := issue(t, ca, false, []string{"x.example.com"}, now.AddDate(1, 0, 0))
	writePEM(t, filepath.Join(root, "c.pem"), leaf)
	writePEM(t, filepath.Join(root, "ca.pem"), ca)
	writeKey(t, filepath.Join(root, "c.key"), leaf)
	writeKey(t, filepath.Join(root, "other.key"), other)

	c, err := CheckPair(filepath.Join(root, "c.pem"), filepath.Join(root, "c.key"), filepath.Join(root, "ca.pem"))
	if err != nil || !c.Covers([]string{"api.example.com"}) || c.Chain == "" {
		t.Fatalf("CheckPair = %+v, %v", c, err)
	}
	if _, err := CheckPair(filepath.Join(root, "c.pem"), filepath.Join(root, "other.key"), ""); err == nil {
		t.Fatal("mismatched key accepted")
	}
	if _, err := CheckPair(filepath.Join(root, "c.key"), filepath.Join(root, "c.key"), ""); err == nil {
		t.Fatal("key file accepted as certificate")
	}
}
