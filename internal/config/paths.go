// Package config holds the fixed filesystem layout and naming conventions
// shared by every deploy-tools command. Production code uses Default();
// tests build their own Paths rooted in a temp dir. There is deliberately no
// environment-variable override: the CI forced command must not be
// steerable by anything the client controls.
//
// The only server-specific settings (TLS certificate and default domain
// suffix) come from /etc/deploy-tools.conf, a root-owned file read by the
// operator CLI. The CI forced command never reads it: deploy, rollback and
// list don't need it.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/vinicgobbi/deploy-tools/internal/tlscert"
)

// File is the optional server config read by the operator CLI.
const File = "/etc/deploy-tools.conf"

// Debian/Ubuntu self-signed certificate (package ssl-cert), used when no
// real certificate covers an app's domains: enough for Apache to start, and
// "deploy-tools app cert <app>" swaps it once the real one exists.
const (
	FallbackCert = "/etc/ssl/certs/ssl-cert-snakeoil.pem"
	FallbackKey  = "/etc/ssl/private/ssl-cert-snakeoil.key"
)

type Paths struct {
	AppsRoot     string // /var/www
	SitesAvail   string // /etc/apache2/sites-available
	SitesEnabled string // /etc/apache2/sites-enabled
	BinDir       string // /usr/local/bin
	DeployHome   string // /home/deploy
	SSHDir       string // /etc/ssh (host keys for DEPLOY_HOST_KEY)

	// DeployHost is how the CI reaches this server (secret DEPLOY_HOST);
	// "" = the machine's hostname.
	DeployHost string

	DeployUser string
	WebGroup   string

	// Fixed certificate for every vhost. Empty (the default): search
	// CertDirs for one that covers the app's domains, falling back to
	// FallbackCert/FallbackKey.
	CertFile  string
	CertKey   string
	CertChain string   // optional: omitted from the vhost when empty
	CertDirs  []string // where to look for certificates (tlscert.DefaultDirs + SSL_SEARCH_DIRS)

	// Base (wildcard) domains: an app gets <subdomain>.<base> when no
	// domain is given. With one base it is the default; with several the
	// CLI asks which one. Empty: --domain is required.
	BaseDomains []string
	BaseCerts   map[string]CertFiles // certificate registered for a base (BASE_CERT), by base
	ConfigFile  string               // File; re-rooted by ForRoot so tests can write it

	KeepReleases int
}

// CertFiles is a certificate as Apache takes it.
type CertFiles struct{ File, Key, Chain string }

func Default() Paths {
	return Paths{
		AppsRoot:     "/var/www",
		SitesAvail:   "/etc/apache2/sites-available",
		SitesEnabled: "/etc/apache2/sites-enabled",
		BinDir:       "/usr/local/bin",
		DeployHome:   "/home/deploy",
		SSHDir:       "/etc/ssh",
		DeployUser:   "deploy",
		WebGroup:     "www-data",
		CertDirs:     append([]string(nil), tlscert.DefaultDirs...),
		ConfigFile:   File,
		KeepReleases: 3,
	}
}

// Load returns Default() with the settings of the config file at path
// applied. A missing file is not an error. The format is KEY=value, one per
// line, # for comments; it is parsed, never sourced.
func Load(path string) (Paths, error) {
	p := Default()
	p.ConfigFile = path
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, found := strings.Cut(line, "=")
		if !found {
			return p, fmt.Errorf("%s:%d: esperado CHAVE=valor", path, n)
		}
		key, val = strings.TrimSpace(key), strings.Trim(strings.TrimSpace(val), `"'`)
		switch key {
		case "BASE_DOMAINS", "DOMAIN_SUFFIX": // DOMAIN_SUFFIX: a single base, from v0.1/v0.2
			for _, d := range splitList(val) {
				if d = NormalizeBase(d); d != "" && !slices.Contains(p.BaseDomains, d) {
					p.BaseDomains = append(p.BaseDomains, d)
				}
			}
		case "BASE_CERT": // <base> <cert> <key> [chain]; one line per base
			f := strings.Fields(val)
			if len(f) < 3 || len(f) > 4 {
				return p, fmt.Errorf("%s:%d: BASE_CERT espera <domínio> <certificado> <chave> [cadeia]", path, n)
			}
			c := CertFiles{File: f[1], Key: f[2]}
			if len(f) == 4 {
				c.Chain = f[3]
			}
			if p.BaseCerts == nil {
				p.BaseCerts = map[string]CertFiles{}
			}
			p.BaseCerts[NormalizeBase(f[0])] = c
		case "DEPLOY_HOST":
			p.DeployHost = val
		case "SSL_CERT_FILE":
			p.CertFile = val
		case "SSL_CERT_KEY":
			p.CertKey = val
		case "SSL_CERT_CHAIN":
			p.CertChain = val
		case "SSL_SEARCH_DIRS":
			// Extra directories, searched before the default ones.
			p.CertDirs = append(splitList(val), tlscert.DefaultDirs...)
		default:
			return p, fmt.Errorf("%s:%d: chave desconhecida %q", path, n, key)
		}
	}
	return p, sc.Err()
}

func splitList(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ':' || r == ' ' || r == ',' })
}

// NormalizeBase turns "*.Example.com", ".example.com" or "example.com."
// into "example.com".
func NormalizeBase(d string) string {
	d = strings.ToLower(strings.TrimSpace(d))
	d = strings.TrimPrefix(strings.TrimPrefix(d, "*"), ".")
	return strings.TrimSuffix(d, ".")
}

// SetBaseDomains writes BASE_DOMAINS and the BASE_CERT lines (for the
// bases in certs) into the config file at path, replacing the old
// BASE_DOMAINS/DOMAIN_SUFFIX/BASE_CERT lines and keeping every other line
// (comments included). The file is created when missing.
func SetBaseDomains(path string, domains []string, certs map[string]CertFiles) error {
	var lines []string
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	replaced := false
	value := []string{"BASE_DOMAINS=" + strings.Join(domains, ",")}
	for _, d := range domains {
		if c, ok := certs[d]; ok && c.File != "" {
			value = append(value, strings.TrimSpace("BASE_CERT="+d+" "+c.File+" "+c.Key+" "+c.Chain))
		}
	}
	for _, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		key, _, _ := strings.Cut(strings.TrimSpace(line), "=")
		switch strings.TrimSpace(key) {
		case "BASE_DOMAINS", "DOMAIN_SUFFIX", "BASE_CERT":
			if !replaced {
				lines = append(lines, value...)
				replaced = true
			}
			continue
		}
		if line != "" || len(lines) > 0 {
			lines = append(lines, line)
		}
	}
	if !replaced {
		if len(lines) == 0 {
			lines = append(lines, "# deploy-tools (veja o README)")
		}
		lines = append(append(lines, "# Domínios coringa: os apps ganham <subdomínio>.<domínio>"), value...)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")+"\n"), mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ForRoot returns Default() with every absolute path re-rooted under root.
// Used by tests.
func ForRoot(root string) Paths {
	p := Default()
	p.AppsRoot = filepath.Join(root, p.AppsRoot)
	p.SitesAvail = filepath.Join(root, p.SitesAvail)
	p.SitesEnabled = filepath.Join(root, p.SitesEnabled)
	p.BinDir = filepath.Join(root, p.BinDir)
	p.DeployHome = filepath.Join(root, p.DeployHome)
	p.SSHDir = filepath.Join(root, p.SSHDir)
	p.ConfigFile = filepath.Join(root, File)
	p.CertDirs = nil
	for _, d := range tlscert.DefaultDirs {
		p.CertDirs = append(p.CertDirs, filepath.Join(root, d))
	}
	return p
}

func (p Paths) AppDir(app string) string      { return filepath.Join(p.AppsRoot, app) }
func (p Paths) ReleasesDir(app string) string { return filepath.Join(p.AppsRoot, app, "releases") }
func (p Paths) SharedDir(app string) string   { return filepath.Join(p.AppsRoot, app, "shared") }
func (p Paths) CurrentLink(app string) string { return filepath.Join(p.AppsRoot, app, "current") }
func (p Paths) VhostFile(app string) string   { return filepath.Join(p.SitesAvail, app+".conf") }
func (p Paths) AuthorizedKeys() string        { return filepath.Join(p.DeployHome, ".ssh", "authorized_keys") }
func (p Paths) BinaryPath() string            { return filepath.Join(p.BinDir, "deploy-tools") }
func (p Paths) WrapperPath() string           { return filepath.Join(p.BinDir, "deploy-ci") }
