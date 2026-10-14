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

	DeployUser string
	WebGroup   string

	// Fixed certificate for every vhost. Empty (the default): search
	// CertDirs for one that covers the app's domains, falling back to
	// FallbackCert/FallbackKey.
	CertFile  string
	CertKey   string
	CertChain string   // optional: omitted from the vhost when empty
	CertDirs  []string // where to look for certificates (tlscert.DefaultDirs + SSL_SEARCH_DIRS)

	DomainSuffix string // appended to the app name when no domain is given; "" = --domain required
	KeepReleases int
}

func Default() Paths {
	return Paths{
		AppsRoot:     "/var/www",
		SitesAvail:   "/etc/apache2/sites-available",
		SitesEnabled: "/etc/apache2/sites-enabled",
		BinDir:       "/usr/local/bin",
		DeployHome:   "/home/deploy",
		DeployUser:   "deploy",
		WebGroup:     "www-data",
		CertDirs:     append([]string(nil), tlscert.DefaultDirs...),
		KeepReleases: 3,
	}
}

// Load returns Default() with the settings of the config file at path
// applied. A missing file is not an error. The format is KEY=value, one per
// line, # for comments; it is parsed, never sourced.
func Load(path string) (Paths, error) {
	p := Default()
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
		case "DOMAIN_SUFFIX":
			if val != "" && !strings.HasPrefix(val, ".") {
				val = "." + val
			}
			p.DomainSuffix = val
		case "SSL_CERT_FILE":
			p.CertFile = val
		case "SSL_CERT_KEY":
			p.CertKey = val
		case "SSL_CERT_CHAIN":
			p.CertChain = val
		case "SSL_SEARCH_DIRS":
			// Extra directories, searched before the default ones.
			p.CertDirs = append(strings.FieldsFunc(val, func(r rune) bool { return r == ':' || r == ' ' || r == ',' }),
				tlscert.DefaultDirs...)
		default:
			return p, fmt.Errorf("%s:%d: chave desconhecida %q", path, n, key)
		}
	}
	return p, sc.Err()
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
