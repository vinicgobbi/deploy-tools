// Package apache generates and edits Apache vhosts and applies changes
// safely (configtest before reload, rollback on failure).
package apache

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"

	"github.com/vinicgobbi/deploy-tools/internal/run"
)

type VhostData struct {
	App, Type string
	Domains   []string // first is the ServerName, the rest ServerAlias
	DocRoot   string   // DocumentRoot (…/current or …/current/public)
	DirRoot   string   // path of the <Directory> block
	CertFile  string
	CertKey   string
	CertChain string
}

// The HTTP vhost redirects each domain to itself over HTTPS, so every
// alias keeps its own address.
var vhostTmpl = template.Must(template.New("vhost").Funcs(template.FuncMap{"join": strings.Join}).Parse(`<VirtualHost *:80>
    ServerName {{index .Domains 0}}
{{- if gt (len .Domains) 1}}
    ServerAlias {{join (slice .Domains 1) " "}}
{{- end}}
    RewriteEngine On
    RewriteRule ^ https://%{HTTP_HOST}%{REQUEST_URI} [R=301,L]
</VirtualHost>

<VirtualHost *:443>
    ServerName {{index .Domains 0}}
{{- if gt (len .Domains) 1}}
    ServerAlias {{join (slice .Domains 1) " "}}
{{- end}}

    DocumentRoot {{.DocRoot}}

    SSLEngine on
    SSLCertificateFile {{.CertFile}}
    SSLCertificateKeyFile {{.CertKey}}
{{- if .CertChain}}
    SSLCertificateChainFile {{.CertChain}}
{{- end}}

    ErrorLog ${APACHE_LOG_DIR}/{{.App}}-error.log
    CustomLog ${APACHE_LOG_DIR}/{{.App}}-access.log combined
</VirtualHost>
{{/*
  +FollowSymLinks is required: DocumentRoot passes through the "current"
  symlink, and for Laravel public/storage -> ../storage/app/public -> the
  "storage" symlink into shared/ is a chain of nested symlinks.
*/}}
<Directory {{.DirRoot}}/>
    Options -Indexes +FollowSymLinks
    AllowOverride All
    Require all granted
{{- if eq .Type "react"}}

    RewriteEngine On
    RewriteBase /
    # Não reescreve arquivos ou diretórios existentes
    RewriteCond %{REQUEST_FILENAME} -f [OR]
    RewriteCond %{REQUEST_FILENAME} -d
    RewriteRule ^ - [L]
    # Todo o resto vai para index.html (SPA)
    RewriteRule ^ index.html [L]
{{- end}}
</Directory>
`))

func Render(d VhostData) (string, error) {
	if len(d.Domains) == 0 {
		return "", errors.New("vhost sem domínio")
	}
	var buf bytes.Buffer
	if err := vhostTmpl.Execute(&buf, d); err != nil {
		return "", err
	}
	return buf.String(), nil
}

var (
	docRootRe = regexp.MustCompile(`(?i)^(\s*DocumentRoot\s+)"?([^"\s]+)"?(.*)$`)
	dirOpenRe = regexp.MustCompile(`(?i)^(\s*<Directory\s+)"?([^"\s>]+)"?(\s*>.*)$`)
	dirEndRe  = regexp.MustCompile(`(?i)^\s*</Directory>`)
	optionsRe = regexp.MustCompile(`(?i)^(\s*)Options\s+(.*)$`)
)

func clean(p string) string { return strings.TrimRight(p, "/") }

// DocumentRoots returns every DocumentRoot value in a vhost file.
func DocumentRoots(content string) []string {
	var out []string
	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		if m := docRootRe.FindStringSubmatch(sc.Text()); m != nil {
			out = append(out, clean(m[2]))
		}
	}
	return out
}

// ServerName returns the first ServerName in a vhost file, or "".
func ServerName(content string) string {
	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) >= 2 && strings.EqualFold(f[0], "ServerName") {
			return f[1]
		}
	}
	return ""
}

var (
	vhostOpenRe  = regexp.MustCompile(`(?i)^\s*<VirtualHost\b`)
	vhostCloseRe = regexp.MustCompile(`(?i)^\s*</VirtualHost>`)
	nameRe       = regexp.MustCompile(`(?i)^(\s*)(ServerName|ServerAlias)\s+(.*)$`)
	redirectRe   = regexp.MustCompile(`(?i)^(\s*Redirect\s+(?:permanent|301)\s+/\s+https?://)([^/\s]+)(.*)$`)
	sslRe        = regexp.MustCompile(`(?i)^(\s*)(SSLCertificateFile|SSLCertificateKeyFile|SSLCertificateChainFile)\s+"?([^"\s]+)"?\s*$`)
)

// Domains returns the ServerName and ServerAliases of a vhost file, in
// order and without repeats, from every <VirtualHost> block.
func Domains(content string) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(content, "\n") {
		m := nameRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		for _, d := range strings.Fields(m[3]) {
			if d = strings.ToLower(d); !seen[d] {
				seen[d] = true
				out = append(out, d)
			}
		}
	}
	return out
}

// SetDomains rewrites the ServerName/ServerAlias lines of every
// <VirtualHost> block to domains (domains[0] as ServerName), and points a
// "Redirect permanent / https://<host>/" at the new ServerName. Everything
// else in the file, hand edits included, is kept.
func SetDomains(content string, domains []string) (string, error) {
	if len(domains) == 0 {
		return "", errors.New("pelo menos um domínio é necessário")
	}
	var out []string
	inVhost, wroteNames, blocks := false, false, 0
	for _, line := range strings.Split(content, "\n") {
		switch {
		case vhostOpenRe.MatchString(line):
			inVhost, wroteNames = true, false
			blocks++
		case vhostCloseRe.MatchString(line):
			inVhost = false
		case inVhost && nameRe.MatchString(line):
			if wroteNames {
				continue // other ServerName/ServerAlias lines of this block
			}
			indent := nameRe.FindStringSubmatch(line)[1]
			out = append(out, indent+"ServerName "+domains[0])
			if len(domains) > 1 {
				out = append(out, indent+"ServerAlias "+strings.Join(domains[1:], " "))
			}
			wroteNames = true
			continue
		case inVhost && redirectRe.MatchString(line):
			m := redirectRe.FindStringSubmatch(line)
			line = m[1] + domains[0] + m[3]
		}
		out = append(out, line)
	}
	if blocks == 0 {
		return "", errors.New("nenhum bloco <VirtualHost> no arquivo")
	}
	return strings.Join(out, "\n"), nil
}

// CertFiles returns the SSLCertificate* values of the first block that
// has them.
func CertFiles(content string) (file, key, chain string) {
	for _, line := range strings.Split(content, "\n") {
		m := sslRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		switch strings.ToLower(m[2]) {
		case "sslcertificatefile":
			if file == "" {
				file = m[3]
			}
		case "sslcertificatekeyfile":
			if key == "" {
				key = m[3]
			}
		case "sslcertificatechainfile":
			if chain == "" {
				chain = m[3]
			}
		}
	}
	return file, key, chain
}

// SetCert points every SSLCertificateFile/KeyFile at file/key and sets,
// adds or removes SSLCertificateChainFile to match chain. It reports how
// many blocks were changed; 0 means the file has no SSL block.
func SetCert(content, file, key, chain string) (string, int) {
	var out []string
	changed := 0
	for _, line := range strings.Split(content, "\n") {
		m := sslRe.FindStringSubmatch(line)
		if m == nil {
			out = append(out, line)
			continue
		}
		indent := m[1]
		switch strings.ToLower(m[2]) {
		case "sslcertificatefile":
			out = append(out, indent+"SSLCertificateFile "+file)
			changed++
		case "sslcertificatekeyfile":
			out = append(out, indent+"SSLCertificateKeyFile "+key)
			if chain != "" {
				out = append(out, indent+"SSLCertificateChainFile "+chain)
			}
		case "sslcertificatechainfile":
			// Re-added right after the key line (or dropped).
		}
	}
	return strings.Join(out, "\n"), changed
}

// FindVhosts returns the files in sitesEnabled (resolved to their real path,
// usually in sites-available) whose DocumentRoot is one of docRoots.
func FindVhosts(sitesEnabled string, docRoots ...string) ([]string, error) {
	entries, err := os.ReadDir(sitesEnabled)
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, d := range docRoots {
		want[clean(d)] = true
	}
	var found []string
	for _, e := range entries {
		path, err := filepath.EvalSymlinks(filepath.Join(sitesEnabled, e.Name()))
		if err != nil {
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, d := range DocumentRoots(string(b)) {
			if want[d] {
				found = append(found, path)
				break
			}
		}
	}
	return found, nil
}

// Repoint rewrites DocumentRoot and <Directory> paths equal to oldRoot to
// newRoot, and makes sure every rewritten <Directory> block allows
// FollowSymLinks (newRoot goes through the "current" symlink). It returns
// the new content and how many DocumentRoot lines changed.
func Repoint(content, oldRoot, newRoot string) (string, int) {
	oldRoot, newRoot = clean(oldRoot), clean(newRoot)
	var out []string
	changed := 0
	inBlock, sawOptions := false, false

	lines := strings.Split(content, "\n")
	for _, line := range lines {
		switch {
		case docRootRe.MatchString(line):
			m := docRootRe.FindStringSubmatch(line)
			if clean(m[2]) == oldRoot {
				line = m[1] + newRoot + m[3]
				changed++
			}
		case dirOpenRe.MatchString(line):
			m := dirOpenRe.FindStringSubmatch(line)
			if clean(m[2]) == oldRoot {
				line = m[1] + newRoot + "/" + m[3]
				inBlock, sawOptions = true, false
			}
		case inBlock && optionsRe.MatchString(line):
			m := optionsRe.FindStringSubmatch(line)
			line = m[1] + "Options " + withFollowSymLinks(m[2])
			sawOptions = true
		case inBlock && dirEndRe.MatchString(line):
			if !sawOptions {
				out = append(out, "    Options +FollowSymLinks")
			}
			inBlock = false
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n"), changed
}

// withFollowSymLinks adds FollowSymLinks to an Options value. Apache 2.4
// rejects mixing +/- options with plain ones, so the style of the existing
// line is kept.
func withFollowSymLinks(opts string) string {
	fields := strings.Fields(opts)
	signed := len(fields) > 0
	for i, f := range fields {
		low := strings.ToLower(strings.TrimLeft(f, "+-"))
		if low == "followsymlinks" || (low == "all" && !strings.HasPrefix(f, "-")) {
			if strings.HasPrefix(f, "-") {
				fields[i] = "+" + f[1:]
			}
			return strings.Join(fields, " ")
		}
		if !strings.HasPrefix(f, "+") && !strings.HasPrefix(f, "-") {
			signed = false
		}
	}
	if signed {
		return strings.Join(append(fields, "+FollowSymLinks"), " ")
	}
	return strings.Join(append(fields, "FollowSymLinks"), " ")
}

// ConfigTest runs apache2ctl configtest; the error carries Apache's output.
func ConfigTest(r run.Runner) error {
	_, err := run.Output(r, run.Cmd{Name: "apache2ctl", Args: []string{"configtest"}})
	return err
}

// ErrConfigAlreadyBroken is returned by Change when the configuration fails
// configtest before anything was touched.
var ErrConfigAlreadyBroken = errors.New("a configuração atual do Apache já tem erro — nada foi alterado; corrija antes de tentar de novo")

// Change makes a config change (e.g. a2ensite) safely: the current config
// must pass configtest before change runs, so a later failure is surely the
// change's fault and undoing it leaves Apache as it was; then Apply tests
// again and reloads, calling undo on failure.
func Change(r run.Runner, change func() error, undo func()) error {
	if err := ConfigTest(r); err != nil {
		return fmt.Errorf("%w: %w", ErrConfigAlreadyBroken, err)
	}
	if err := change(); err != nil {
		return err
	}
	return Apply(r, undo)
}

// Apply runs configtest and, only if it passes, reloads Apache. On any
// failure it calls rollback (which should undo the config change) and
// returns the error with Apache's output.
func Apply(r run.Runner, rollback func()) error {
	if err := ConfigTest(r); err != nil {
		if rollback != nil {
			rollback()
		}
		return fmt.Errorf("configuração do Apache inválida, alteração desfeita: %w", err)
	}
	if _, err := run.Output(r, run.Cmd{Name: "systemctl", Args: []string{"reload", "apache2"}}); err != nil {
		if rollback != nil {
			rollback()
			_, _ = run.Output(r, run.Cmd{Name: "systemctl", Args: []string{"reload", "apache2"}})
		}
		return fmt.Errorf("falha ao recarregar o Apache, alteração desfeita: %w", err)
	}
	return nil
}

// EnsureRewrite enables mod_rewrite when missing; reports whether it did.
func EnsureRewrite(r run.Runner) (bool, error) {
	out, err := run.Output(r, run.Cmd{Name: "apache2ctl", Args: []string{"-M"}})
	if err == nil && strings.Contains(out, "rewrite_module") {
		return false, nil
	}
	if _, err := run.Output(r, run.Cmd{Name: "a2enmod", Args: []string{"rewrite"}}); err != nil {
		return false, err
	}
	return true, nil
}

// WriteFileAtomic writes via temp file + rename, keeping mode.
func WriteFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
