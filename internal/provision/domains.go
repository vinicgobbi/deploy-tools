package provision

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/vinicgobbi/deploy-tools/internal/apache"
	"github.com/vinicgobbi/deploy-tools/internal/config"
	"github.com/vinicgobbi/deploy-tools/internal/tlscert"
	"github.com/vinicgobbi/deploy-tools/internal/validate"
)

// CertChoice is the certificate a vhost will use and where it came from.
type CertChoice struct {
	File, Key, Chain string

	Source   string    // human description of how it was chosen
	Fallback bool      // the provisional self-signed certificate
	Missing  []string  // domains it does not cover
	NotAfter time.Time // zero when unknown
}

// NormalizeDomains lowercases, trims and de-duplicates, keeping order.
func NormalizeDomains(ds []string) []string {
	var out []string
	for _, d := range ds {
		for _, part := range strings.Split(d, ",") {
			part = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(part), "."))
			if part != "" && !slices.Contains(out, part) {
				out = append(out, part)
			}
		}
	}
	return out
}

func validateDomains(ds []string) error {
	if len(ds) == 0 {
		return validate.Domain("")
	}
	for _, d := range ds {
		if err := validate.Domain(d); err != nil {
			return err
		}
	}
	return nil
}

func (p *Provisioner) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// ChooseCert picks the certificate for domains, in this order: the one the
// operator passed (override), the fixed one in /etc/deploy-tools.conf, one
// found on disk that covers every domain, and finally the self-signed
// fallback so Apache can still start.
func (p *Provisioner) ChooseCert(domains []string, override CertChoice) (CertChoice, error) {
	inspect := func(c CertChoice) CertChoice {
		if info, err := tlscert.Inspect(c.File); err == nil {
			c.Missing, c.NotAfter = tlscert.Missing(info, domains), info.NotAfter
		}
		return c
	}
	switch {
	case override.File != "":
		if override.Key == "" {
			return CertChoice{}, errors.New("informe também a chave privada (--key)")
		}
		for _, f := range []string{override.File, override.Key, override.Chain} {
			if f != "" && !exists(f) {
				return CertChoice{}, fmt.Errorf("arquivo não encontrado: %s", f)
			}
		}
		override.Source = "informado"
		return inspect(override), nil
	case p.Paths.CertFile != "":
		return inspect(CertChoice{
			File: p.Paths.CertFile, Key: p.Paths.CertKey, Chain: p.Paths.CertChain,
			Source: "fixo em " + config.File,
		}), nil
	}
	if c, ok := tlscert.Find(p.Paths.CertDirs, domains, p.now()); ok {
		return CertChoice{File: c.File, Key: c.Key, Chain: c.Chain, Source: "encontrado automaticamente", NotAfter: c.NotAfter}, nil
	}
	return CertChoice{
		File: config.FallbackCert, Key: config.FallbackKey,
		Source: "autoassinado provisório (nenhum certificado no servidor cobre os domínios)", Fallback: true, Missing: domains,
	}, nil
}

func (p *Provisioner) sayCert(c CertChoice) {
	p.say("Certificado %s: %s", c.Source, c.File)
	if !c.NotAfter.IsZero() {
		p.say("Válido até %s.", c.NotAfter.Format("02/01/2006"))
	}
	if len(c.Missing) > 0 && !c.Fallback {
		p.say("Atenção: o certificado não cobre %s.", strings.Join(c.Missing, ", "))
	}
}

// Domains returns the domains of app's Apache site.
func (p *Provisioner) Domains(app string) ([]string, error) {
	info, err := p.vhostOf(app)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(info.Vhost)
	if err != nil {
		return nil, err
	}
	return apache.Domains(string(b)), nil
}

// AddDomains adds domains to app's site (the first domain stays the
// ServerName).
func (p *Provisioner) AddDomains(app string, add []string) error {
	cur, err := p.Domains(app)
	if err != nil {
		return err
	}
	add = NormalizeDomains(add)
	if err := validateDomains(add); err != nil {
		return err
	}
	next := NormalizeDomains(append(slices.Clone(cur), add...))
	if len(next) == len(cur) {
		p.say("Nada a fazer: %s já estão no site.", strings.Join(add, ", "))
		return nil
	}
	return p.SetDomains(app, next)
}

// RemoveDomains removes domains from app's site. Removing the ServerName
// promotes the next domain; the last domain cannot be removed.
func (p *Provisioner) RemoveDomains(app string, rm []string) error {
	cur, err := p.Domains(app)
	if err != nil {
		return err
	}
	rm = NormalizeDomains(rm)
	for _, d := range rm {
		if !slices.Contains(cur, d) {
			return fmt.Errorf("%s não é um domínio de %s (domínios: %s)", d, app, strings.Join(cur, ", "))
		}
	}
	next := slices.DeleteFunc(slices.Clone(cur), func(d string) bool { return slices.Contains(rm, d) })
	if len(next) == 0 {
		return errors.New("o site precisa de pelo menos um domínio — para tirá-lo do ar, use `app disable`")
	}
	return p.SetDomains(app, next)
}

// SetDomains replaces the domains of app's site. When the current
// certificate does not cover the new set, it looks for one that does and
// switches to it. An active site is changed with configtest before and
// after, and restored on failure.
func (p *Provisioner) SetDomains(app string, domains []string) error {
	domains = NormalizeDomains(domains)
	if err := validateDomains(domains); err != nil {
		return err
	}
	info, err := p.vhostOf(app)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(info.Vhost)
	if err != nil {
		return err
	}
	old := string(b)
	content, err := apache.SetDomains(old, domains)
	if err != nil {
		return fmt.Errorf("%s: %w", info.Vhost, err)
	}

	if file, _, _ := apache.CertFiles(content); file != "" {
		var missing []string
		if cur, err := tlscert.Inspect(file); err == nil {
			missing = tlscert.Missing(cur, domains)
		}
		if len(missing) > 0 || file == config.FallbackCert {
			c, err := p.ChooseCert(domains, CertChoice{})
			if err != nil {
				return err
			}
			if !c.Fallback && c.File != file {
				content, _ = apache.SetCert(content, c.File, c.Key, c.Chain)
				p.sayCert(c)
			} else if len(missing) > 0 {
				p.say("Atenção: o certificado atual (%s) não cobre %s. Instale um que cubra e rode `deploy-tools app cert %s`.",
					file, strings.Join(missing, ", "), app)
			}
		}
	}

	if err := p.rewriteVhost(info, old, content); err != nil {
		return err
	}
	p.say("Domínios de %s: %s", app, strings.Join(domains, ", "))
	return nil
}

// UpdateCert points app's site at override, or at the best certificate
// found for its domains. It never swaps a real certificate for the
// self-signed fallback.
func (p *Provisioner) UpdateCert(app string, override CertChoice) (CertChoice, error) {
	info, err := p.vhostOf(app)
	if err != nil {
		return CertChoice{}, err
	}
	b, err := os.ReadFile(info.Vhost)
	if err != nil {
		return CertChoice{}, err
	}
	old := string(b)
	domains := apache.Domains(old)
	c, err := p.ChooseCert(domains, override)
	if err != nil {
		return CertChoice{}, err
	}
	curFile, curKey, curChain := apache.CertFiles(old)
	if c.Fallback && curFile != "" && curFile != config.FallbackCert {
		return c, fmt.Errorf("nenhum certificado no servidor cobre %s — o atual (%s) foi mantido. "+
			"Emita um (ex.: certbot certonly) ou informe --cert/--key", strings.Join(domains, ", "), curFile)
	}
	if c.File == curFile && c.Key == curKey && c.Chain == curChain {
		p.say("O site já usa %s.", c.File)
		return c, nil
	}
	content, n := apache.SetCert(old, c.File, c.Key, c.Chain)
	if n == 0 {
		return c, fmt.Errorf("%s não tem bloco SSL (SSLCertificateFile) para atualizar", info.Vhost)
	}
	if err := p.rewriteVhost(info, old, content); err != nil {
		return c, err
	}
	p.sayCert(c)
	return c, nil
}

func (p *Provisioner) vhostOf(app string) (AppInfo, error) {
	if err := validate.AppName(app); err != nil {
		return AppInfo{}, err
	}
	info, err := p.App(app)
	if err != nil {
		return info, err
	}
	if info.Vhost == "" {
		return info, fmt.Errorf("%s não tem site no Apache", app)
	}
	return info, nil
}

// rewriteVhost writes content over the site file. An active site goes
// through apache.Change (configtest, reload, restore old on failure).
func (p *Provisioner) rewriteVhost(info AppInfo, old, content string) error {
	fi, err := os.Stat(info.Vhost)
	if err != nil {
		return err
	}
	write := func(s string) error { return apache.WriteFileAtomic(info.Vhost, []byte(s), fi.Mode().Perm()) }
	if !info.VhostEnabled {
		return write(content)
	}
	return apache.Change(p.Runner, func() error { return write(content) }, func() { _ = write(old) })
}
