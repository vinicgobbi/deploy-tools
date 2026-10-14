// Package tlscert finds TLS certificates already on the server for a set of
// domains. It looks where certificates usually live (Let's Encrypt, the
// Debian and RHEL default directories, Apache's own ssl/ folders, plus any
// extra directory the operator configures), parses every certificate and
// only accepts one that covers all the domains, is currently valid and has
// a matching private key on disk.
package tlscert

import (
	"bytes"
	"crypto"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DefaultDirs are searched for certificates, in this order of preference.
// Let's Encrypt is handled specially (fullchain.pem + privkey.pem per
// lineage under live/); the others are scanned for loose files.
var DefaultDirs = []string{
	"/etc/letsencrypt/live",
	"/etc/ssl/certs",
	"/etc/ssl/private",
	"/etc/ssl",
	"/etc/pki/tls/certs",
	"/etc/pki/tls/private",
	"/etc/apache2/ssl",
	"/etc/httpd/ssl",
}

// Cert is a certificate usable in an Apache vhost.
type Cert struct {
	File  string // leaf certificate (may contain the chain too)
	Key   string // matching private key
	Chain string // separate CA bundle, "" when File already has it

	Names     []string // DNS names it covers
	NotAfter  time.Time
	SelfSign  bool
	LetsCrypt bool
}

// Covers reports whether the certificate is valid for every domain.
func (c Cert) Covers(domains []string) bool {
	return len(Missing(c, domains)) == 0
}

// Missing lists the domains the certificate does not cover.
func Missing(c Cert, domains []string) []string {
	leaf := &x509.Certificate{DNSNames: c.Names}
	var out []string
	for _, d := range domains {
		if leaf.VerifyHostname(d) != nil {
			out = append(out, d)
		}
	}
	return out
}

func (c Cert) String() string {
	s := c.File
	if c.Chain != "" {
		s += " (cadeia: " + c.Chain + ")"
	}
	return s
}

// Scan returns every leaf certificate with a matching private key found in
// dirs, best first (Let's Encrypt and CA-issued before self-signed, then
// the one that expires last). Unreadable files are skipped: on a server
// some private directories are root-only.
func Scan(dirs []string) []Cert {
	var certFiles []string
	keys := map[string]crypto.PublicKey{} // key file -> public key
	var leList []Cert
	seen := map[string]bool{}

	for _, dir := range dirs {
		if filepath.Base(dir) == "live" {
			leList = append(leList, scanLetsEncrypt(dir)...)
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			path := filepath.Join(dir, e.Name())
			fi, err := os.Stat(path) // follows symlinks
			if err != nil || fi.IsDir() || fi.Size() > 1<<20 || seen[path] {
				continue
			}
			seen[path] = true
			b, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			switch {
			case bytes.Contains(b, []byte("PRIVATE KEY-----")):
				if pub, err := publicKeyOf(b); err == nil {
					keys[path] = pub
				}
			case bytes.Contains(b, []byte("-----BEGIN CERTIFICATE-----")):
				certFiles = append(certFiles, path)
			}
		}
	}

	var bundles []*x509.Certificate
	bundleFile := map[*x509.Certificate]string{}
	var leaves []struct {
		path  string
		chain []*x509.Certificate
	}
	for _, f := range certFiles {
		chain, err := parseCerts(f)
		if err != nil || len(chain) == 0 {
			continue
		}
		if chain[0].IsCA {
			// CA bundles (ca-bundle.crt, chain.pem...) and the system trust
			// store: candidates for a separate SSLCertificateChainFile.
			bundles = append(bundles, chain[0])
			bundleFile[chain[0]] = f
			continue
		}
		leaves = append(leaves, struct {
			path  string
			chain []*x509.Certificate
		}{f, chain})
	}

	out := leList
	for _, l := range leaves {
		leaf := l.chain[0]
		keyFile := ""
		for _, kf := range sortedKeys(keys) {
			if samePublicKey(keys[kf], leaf.PublicKey) {
				keyFile = kf
				break
			}
		}
		if keyFile == "" {
			continue
		}
		c := certOf(leaf)
		c.File, c.Key = l.path, keyFile
		if len(l.chain) == 1 && !c.SelfSign {
			// Prefer a bundle next to the certificate over the trust store.
			for _, b := range sortedBundles(bundles, bundleFile, filepath.Dir(l.path)) {
				if leaf.CheckSignatureFrom(b) == nil {
					c.Chain = bundleFile[b]
					break
				}
			}
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].SelfSign != out[j].SelfSign {
			return !out[i].SelfSign
		}
		if out[i].LetsCrypt != out[j].LetsCrypt {
			return out[i].LetsCrypt
		}
		return out[i].NotAfter.After(out[j].NotAfter)
	})
	return out
}

// Find returns the best certificate in dirs that covers every domain and is
// valid at now.
func Find(dirs, domains []string, now time.Time) (Cert, bool) {
	for _, c := range Scan(dirs) {
		if now.Before(c.NotAfter) && c.Covers(domains) {
			return c, true
		}
	}
	return Cert{}, false
}

// Inspect reads a certificate that the operator pointed at explicitly.
func Inspect(file string) (Cert, error) {
	chain, err := parseCerts(file)
	if err != nil {
		return Cert{}, err
	}
	if len(chain) == 0 {
		return Cert{}, fmt.Errorf("%s: nenhum certificado PEM", file)
	}
	c := certOf(chain[0])
	c.File = file
	return c, nil
}

func scanLetsEncrypt(live string) []Cert {
	entries, err := os.ReadDir(live)
	if err != nil {
		return nil
	}
	var out []Cert
	for _, e := range entries {
		dir := filepath.Join(live, e.Name())
		full, key := filepath.Join(dir, "fullchain.pem"), filepath.Join(dir, "privkey.pem")
		chain, err := parseCerts(full)
		if err != nil || len(chain) == 0 {
			continue
		}
		if _, err := os.Stat(key); err != nil {
			continue
		}
		c := certOf(chain[0])
		c.File, c.Key, c.LetsCrypt = full, key, true
		out = append(out, c)
	}
	return out
}

func certOf(leaf *x509.Certificate) Cert {
	names := append([]string(nil), leaf.DNSNames...)
	if len(names) == 0 && leaf.Subject.CommonName != "" {
		names = []string{leaf.Subject.CommonName}
	}
	return Cert{
		Names:    names,
		NotAfter: leaf.NotAfter,
		SelfSign: bytes.Equal(leaf.RawIssuer, leaf.RawSubject) && leaf.CheckSignature(leaf.SignatureAlgorithm, leaf.RawTBSCertificate, leaf.Signature) == nil,
	}
}

func parseCerts(path string) ([]*x509.Certificate, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []*x509.Certificate
	for {
		var blk *pem.Block
		blk, b = pem.Decode(b)
		if blk == nil {
			break
		}
		if blk.Type != "CERTIFICATE" {
			continue
		}
		c, err := x509.ParseCertificate(blk.Bytes)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		out = append(out, c)
	}
	return out, nil
}

type publicKeyer interface{ Public() crypto.PublicKey }

func publicKeyOf(b []byte) (crypto.PublicKey, error) {
	for {
		var blk *pem.Block
		blk, b = pem.Decode(b)
		if blk == nil {
			return nil, errors.New("nenhuma chave privada")
		}
		if !strings.HasSuffix(blk.Type, "PRIVATE KEY") {
			continue
		}
		var k any
		var err error
		switch blk.Type {
		case "RSA PRIVATE KEY":
			k, err = x509.ParsePKCS1PrivateKey(blk.Bytes)
		case "EC PRIVATE KEY":
			k, err = x509.ParseECPrivateKey(blk.Bytes)
		default:
			k, err = x509.ParsePKCS8PrivateKey(blk.Bytes)
		}
		if err != nil {
			return nil, err
		}
		if pk, ok := k.(publicKeyer); ok {
			return pk.Public(), nil
		}
		return nil, errors.New("tipo de chave não suportado")
	}
}

type equaler interface{ Equal(crypto.PublicKey) bool }

func samePublicKey(a, b crypto.PublicKey) bool {
	ea, ok := a.(equaler)
	return ok && ea.Equal(b)
}

func sortedKeys(m map[string]crypto.PublicKey) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedBundles(bs []*x509.Certificate, file map[*x509.Certificate]string, near string) []*x509.Certificate {
	out := append([]*x509.Certificate(nil), bs...)
	sort.SliceStable(out, func(i, j int) bool {
		ni, nj := filepath.Dir(file[out[i]]) == near, filepath.Dir(file[out[j]]) == near
		if ni != nj {
			return ni
		}
		return file[out[i]] < file[out[j]]
	})
	return out
}
