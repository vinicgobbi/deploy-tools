package provision

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/vinicgobbi/deploy-tools/internal/config"
	"github.com/vinicgobbi/deploy-tools/internal/tlscert"
	"github.com/vinicgobbi/deploy-tools/internal/validate"
)

func registered(bases []string) string {
	if len(bases) == 0 {
		return "nenhum cadastrado"
	}
	return "cadastrados: " + strings.Join(bases, ", ")
}

// WildcardCert returns a certificate that covers any subdomain of base
// (i.e. *.base): the one registered for it, the fixed one, or one found on
// the server.
func (p *Provisioner) WildcardCert(base string) (tlscert.Cert, bool) {
	probe := []string{"deploy-tools-probe." + base}
	if bc, ok := p.Paths.BaseCerts[base]; ok {
		if c, err := tlscert.Inspect(bc.File); err == nil && p.now().Before(c.NotAfter) && c.Covers(probe) {
			c.Key, c.Chain = bc.Key, bc.Chain
			return c, true
		}
	}
	if p.Paths.CertFile != "" {
		if c, err := tlscert.Inspect(p.Paths.CertFile); err == nil && c.Covers([]string{"deploy-tools-probe." + base}) {
			return c, true
		}
		return tlscert.Cert{}, false
	}
	return tlscert.Find(p.Paths.CertDirs, []string{"deploy-tools-probe." + base}, p.now())
}

// CheckBaseCert validates a certificate for base: the key matches, it is
// valid now and covers *.base.
func (p *Provisioner) CheckBaseCert(base string, c config.CertFiles) error {
	info, err := tlscert.CheckPair(c.File, c.Key, c.Chain)
	if err != nil {
		return err
	}
	if !p.now().Before(info.NotAfter) {
		return fmt.Errorf("%s expirou em %s", c.File, info.NotAfter.Format("02/01/2006"))
	}
	if !info.Covers([]string{"deploy-tools-probe." + base}) {
		return fmt.Errorf("%s não é coringa de %s (cobre: %s)", c.File, base, strings.Join(info.Names, ", "))
	}
	return nil
}

// AddBaseDomain registers a base (wildcard) domain in the config file,
// with its certificate when cert.File is set (validated first; it also
// replaces the certificate of a base already registered). It reports
// whether a wildcard certificate for the base is available.
func (p *Provisioner) AddBaseDomain(d string, cert config.CertFiles) (hasCert bool, err error) {
	d = config.NormalizeBase(d)
	if err := validate.Domain(d); err != nil {
		return false, err
	}
	if cert.File != "" {
		if err := p.CheckBaseCert(d, cert); err != nil {
			return false, err
		}
	}
	known := slices.Contains(p.Paths.BaseDomains, d)
	if !known || cert.File != "" {
		next := slices.Clone(p.Paths.BaseDomains)
		if !known {
			next = append(next, d)
		}
		certs := maps.Clone(p.Paths.BaseCerts)
		if certs == nil {
			certs = map[string]config.CertFiles{}
		}
		if cert.File != "" {
			certs[d] = cert
		}
		if err := config.SetBaseDomains(p.Paths.ConfigFile, next, certs); err != nil {
			return false, err
		}
		p.Paths.BaseDomains, p.Paths.BaseCerts = next, certs
	}
	switch {
	case !known:
		p.say("Domínio coringa %s cadastrado em %s.", d, p.Paths.ConfigFile)
	case cert.File == "":
		p.say("%s já está cadastrado.", d)
	}
	if cert.File != "" {
		p.say("Certificado de *.%s cadastrado: %s.", d, cert.File)
	}
	c, ok := p.WildcardCert(d)
	switch {
	case ok && cert.File == "":
		p.say("Certificado coringa encontrado: %s (válido até %s).", c.File, c.NotAfter.Format("02/01/2006"))
	case !ok:
		p.say("Nenhum certificado *.%s no servidor: os apps em %s ficam só em HTTP até ter um "+
			"(ex.: certbot certonly --manual --preferred-challenges dns -d '*.%s').", d, d, d)
	}
	return ok, nil
}

// RemoveBaseDomain unregisters a base domain. Existing sites keep their
// domains: only new apps stop being offered it.
func (p *Provisioner) RemoveBaseDomain(d string) error {
	d = config.NormalizeBase(d)
	if !slices.Contains(p.Paths.BaseDomains, d) {
		return fmt.Errorf("%s não está cadastrado (%s)", d, registered(p.Paths.BaseDomains))
	}
	next := slices.DeleteFunc(slices.Clone(p.Paths.BaseDomains), func(x string) bool { return x == d })
	certs := maps.Clone(p.Paths.BaseCerts)
	delete(certs, d)
	if err := config.SetBaseDomains(p.Paths.ConfigFile, next, certs); err != nil {
		return err
	}
	p.Paths.BaseDomains, p.Paths.BaseCerts = next, certs
	p.say("Domínio coringa %s removido. Os sites que já usam %s não mudam.", d, d)
	return nil
}
