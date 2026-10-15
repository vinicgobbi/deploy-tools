package provision

import (
	"fmt"
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

// WildcardCert returns a certificate on the server that covers any
// subdomain of base (i.e. *.base), if there is one.
func (p *Provisioner) WildcardCert(base string) (tlscert.Cert, bool) {
	if p.Paths.CertFile != "" {
		if c, err := tlscert.Inspect(p.Paths.CertFile); err == nil && c.Covers([]string{"deploy-tools-probe." + base}) {
			return c, true
		}
		return tlscert.Cert{}, false
	}
	return tlscert.Find(p.Paths.CertDirs, []string{"deploy-tools-probe." + base}, p.now())
}

// AddBaseDomain registers a base (wildcard) domain in the config file.
func (p *Provisioner) AddBaseDomain(d string) error {
	d = config.NormalizeBase(d)
	if err := validate.Domain(d); err != nil {
		return err
	}
	if slices.Contains(p.Paths.BaseDomains, d) {
		p.say("%s já está cadastrado.", d)
	} else {
		next := append(slices.Clone(p.Paths.BaseDomains), d)
		if err := config.SetBaseDomains(p.Paths.ConfigFile, next); err != nil {
			return err
		}
		p.Paths.BaseDomains = next
		p.say("Domínio coringa %s cadastrado em %s.", d, p.Paths.ConfigFile)
	}
	if c, ok := p.WildcardCert(d); ok {
		p.say("Certificado coringa encontrado: %s (válido até %s).", c.File, c.NotAfter.Format("02/01/2006"))
	} else {
		p.say("Atenção: nenhum certificado *.%s no servidor. Emita um coringa (ex.: certbot certonly --manual "+
			"--preferred-challenges dns -d '*.%s') ou cada app usará um certificado próprio.", d, d)
	}
	return nil
}

// RemoveBaseDomain unregisters a base domain. Existing sites keep their
// domains: only new apps stop being offered it.
func (p *Provisioner) RemoveBaseDomain(d string) error {
	d = config.NormalizeBase(d)
	if !slices.Contains(p.Paths.BaseDomains, d) {
		return fmt.Errorf("%s não está cadastrado (%s)", d, registered(p.Paths.BaseDomains))
	}
	next := slices.DeleteFunc(slices.Clone(p.Paths.BaseDomains), func(x string) bool { return x == d })
	if err := config.SetBaseDomains(p.Paths.ConfigFile, next); err != nil {
		return err
	}
	p.Paths.BaseDomains = next
	p.say("Domínio coringa %s removido. Os sites que já usam %s não mudam.", d, d)
	return nil
}
