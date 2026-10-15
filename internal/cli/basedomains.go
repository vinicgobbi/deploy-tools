package cli

import (
	"errors"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/vinicgobbi/deploy-tools/internal/config"
	"github.com/vinicgobbi/deploy-tools/internal/provision"
	"github.com/vinicgobbi/deploy-tools/internal/tlscert"
	"github.com/vinicgobbi/deploy-tools/internal/validate"
)

const otherDomain = "\x00outro"

func validDomains(s string, optional bool) error {
	ds := provision.NormalizeDomains([]string{s})
	if len(ds) == 0 {
		if optional {
			return nil
		}
		return validate.Domain("")
	}
	for _, d := range ds {
		if err := validate.Domain(d); err != nil {
			return err
		}
	}
	return nil
}

// chooseDomains asks which domain(s) an app uses: a subdomain of one of the
// registered base (wildcard) domains, or any domain typed in. app gives the
// suggested subdomain ("" = none); extras also asks for more domains.
func chooseDomains(env *Env, app string, extras bool) ([]string, error) {
	bases := env.Paths.BaseDomains
	if len(bases) > 0 {
		sub := provision.SubdomainFor(app)
		shown := sub
		if shown == "" {
			shown = "<subdomínio>"
		}
		choice := bases[0]
		opts := make([]huh.Option[string], 0, len(bases)+1)
		for _, b := range bases {
			label := shown + "." + b
			if _, ok := env.P.WildcardCert(b); ok {
				label += "   (certificado coringa ok)"
			}
			opts = append(opts, huh.NewOption(label, b))
		}
		opts = append(opts, huh.NewOption("Outro domínio (digitar)", otherDomain))
		if err := env.UI.Select("Qual domínio usar?", &choice, opts...); err != nil {
			return nil, err
		}
		if choice != otherDomain {
			var s string
			if err := env.UI.Input("Subdomínio de "+choice, sub, &s, func(s string) error {
				s = strings.Trim(strings.TrimSpace(s), ".")
				if s == "" && sub != "" {
					return nil
				}
				return validate.Domain(strings.ToLower(s) + "." + choice)
			}); err != nil {
				return nil, err
			}
			if s = strings.ToLower(strings.Trim(strings.TrimSpace(s), ".")); s == "" {
				s = sub
			}
			ds := []string{s + "." + choice}
			if extras {
				var more string
				if err := env.UI.Input("Outros domínios (opcional, separados por vírgula)", "www."+ds[0], &more,
					func(s string) error { return validDomains(s, true) }); err != nil {
					return nil, err
				}
				ds = provision.NormalizeDomains(append(ds, more))
			}
			return ds, nil
		}
	}
	var s string
	if err := env.UI.Input("Domínios (separados por vírgula; o 1º é o principal)", "app.example.com, www.app.example.com", &s,
		func(s string) error { return validDomains(s, false) }); err != nil {
		return nil, err
	}
	return provision.NormalizeDomains([]string{s}), nil
}

// askCert offers to register certificate files already on the server;
// a zero CertChoice means the operator declined.
func askCert(env *Env, question, description string) (provision.CertChoice, error) {
	ok, err := env.UI.ConfirmDefault(question, description, false)
	if err != nil || !ok {
		return provision.CertChoice{}, err
	}
	var c provision.CertChoice
	if err := env.UI.Input("Certificado (arquivo PEM, com a cadeia ou não)", "/etc/ssl/certs/exemplo.crt", &c.File, fileExists); err != nil {
		return c, err
	}
	if err := env.UI.Input("Chave privada", "/etc/ssl/private/exemplo.key", &c.Key, fileExists); err != nil {
		return c, err
	}
	if err := env.UI.Input("Cadeia / CA bundle (opcional)", "", &c.Chain, func(s string) error {
		if s == "" {
			return nil
		}
		return fileExists(s)
	}); err != nil {
		return c, err
	}
	if _, err := tlscert.CheckPair(c.File, c.Key, c.Chain); err != nil {
		return provision.CertChoice{}, err
	}
	return c, nil
}

// addBaseDomain registers d and, on a terminal with no certificate for
// *.d, offers to register one.
func addBaseDomain(env *Env, d string, cert config.CertFiles) error {
	ok, err := env.P.AddBaseDomain(d, cert)
	if err != nil || ok || cert.File != "" || !env.UI.Interactive {
		return err
	}
	d = config.NormalizeBase(d)
	c, err := askCert(env, "Cadastrar um certificado TLS para *."+d+"?",
		"Arquivos PEM já no servidor. Sem certificado, os apps em "+d+" ficam só em HTTP.")
	if err != nil || c.File == "" {
		return err
	}
	_, err = env.P.AddBaseDomain(d, config.CertFiles{File: c.File, Key: c.Key, Chain: c.Chain})
	return err
}

// askCertForApp offers a certificate when none covers the new app's
// domains (the site would be HTTP only).
func askCertForApp(env *Env, o *provision.CreateOptions) error {
	if !env.UI.Interactive || o.Cert.File != "" {
		return nil
	}
	o.Defaults(env.Paths.BaseDomains)
	if len(o.Domains) == 0 {
		return nil
	}
	if c, err := env.P.ChooseCert(o.Domains, provision.CertChoice{}); err != nil || !c.Fallback {
		return err
	}
	c, err := askCert(env, "Nenhum certificado cobre "+strings.Join(o.Domains, ", ")+". Informar um agora?",
		"Arquivos PEM já no servidor. Sem certificado, o site fica só em HTTP (porta 80) até você rodar `app cert`.")
	if err != nil {
		return err
	}
	o.Cert = c
	return nil
}

func doBaseDomains(env *Env) error {
	if len(env.Paths.BaseDomains) == 0 {
		env.UI.Println("Nenhum domínio coringa cadastrado.")
		env.UI.Hint("Cadastre com: sudo deploy-tools server domain add vinicgobbi.dev.br")
		return nil
	}
	rows := [][]string{{"DOMÍNIO", "APPS GANHAM", "CERTIFICADO CORINGA"}}
	for _, b := range env.Paths.BaseDomains {
		cert := "nenhum *." + b + " no servidor"
		if c, ok := env.P.WildcardCert(b); ok {
			cert = c.File + " (até " + c.NotAfter.Format("02/01/2006") + ")"
		}
		rows = append(rows, []string{b, "<app>." + b, cert})
	}
	printTable(env, rows)
	return nil
}

// syncPaths copies the provisioner's paths (changed by base domain edits)
// back into env.
func syncPaths(env *Env) { env.Paths = env.P.Paths }

func baseDomainCmd(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "domain",
		Short: "Domínios coringa: base para o endereço dos apps (<app>.<domínio>)",
		Long: `Cadastra domínios base (coringa), como vinicgobbi.dev.br. Ao criar um app
sem --domain, ele ganha <subdomínio>.<domínio base>: com um só cadastrado,
ele é usado direto; com vários, o deploy-tools pergunta qual usar (ou use
--base e --sub).

Ficam em BASE_DOMAINS de ` + config.File + `. Ao cadastrar, o comando diz
se há um certificado *.<domínio> no servidor (ou grava o que você informar
com --cert/--key): com ele, todo app novo no domínio já sai com HTTPS.
Sem certificado, os apps ficam só em HTTP até você rodar "app cert".`,
	}
	cmd.AddCommand(&cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "Lista os domínios coringa",
		Args:    cobra.NoArgs,
		RunE:    func(*cobra.Command, []string) error { return doBaseDomains(env) },
	})
	var cert provision.CertChoice
	add := &cobra.Command{
		Use:   "add <domínio>...",
		Short: "Cadastra um ou mais domínios coringa (e o certificado *.<domínio>)",
		Long: `Cadastra domínios coringa. Com --cert/--key, grava também o certificado
*.<domínio> (validado: a chave precisa ser dele, ele precisa estar válido e
ser coringa do domínio). Num domínio já cadastrado, --cert troca o
certificado. Sem --cert, num terminal, se não houver certificado *.<domínio>
no servidor, o comando pergunta se você quer informar um.`,
		Example: `  sudo deploy-tools server domain add vinicgobbi.dev.br
  sudo deploy-tools server domain add '*.example.com'
  sudo deploy-tools server domain add vinicgobbi.dev.br --cert /etc/ssl/certs/wild.crt --key /etc/ssl/private/wild.key`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := env.RequireRoot(); err != nil {
				return err
			}
			if cert.File != "" && len(args) > 1 {
				return errors.New("--cert vale para um domínio só")
			}
			defer syncPaths(env)
			for _, d := range args {
				if err := addBaseDomain(env, d, config.CertFiles{File: cert.File, Key: cert.Key, Chain: cert.Chain}); err != nil {
					return err
				}
			}
			return nil
		},
	}
	certFlags(add, &cert)
	cmd.AddCommand(add)
	cmd.AddCommand(&cobra.Command{
		Use:     "remove <domínio>...",
		Aliases: []string{"rm"},
		Short:   "Remove domínios coringa (os sites que já usam não mudam)",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := env.RequireRoot(); err != nil {
				return err
			}
			defer syncPaths(env)
			for _, d := range args {
				if err := env.P.RemoveBaseDomain(d); err != nil {
					return err
				}
			}
			return nil
		},
	})
	return cmd
}

func iBaseDomains(env *Env) error {
	if err := doBaseDomains(env); err != nil {
		return err
	}
	env.UI.Println()
	action := "add"
	opts := []huh.Option[string]{huh.NewOption("Cadastrar domínio coringa", "add")}
	if len(env.Paths.BaseDomains) > 0 {
		opts = append(opts, huh.NewOption("Remover domínio coringa", "remove"))
	}
	opts = append(opts, huh.NewOption("Voltar", "back"))
	if err := env.UI.Select("O que fazer?", &action, opts...); err != nil {
		return err
	}
	defer syncPaths(env)
	switch action {
	case "add":
		var d string
		if err := env.UI.Input("Domínio coringa", "vinicgobbi.dev.br", &d, func(s string) error {
			return validate.Domain(config.NormalizeBase(s))
		}); err != nil {
			return err
		}
		return addBaseDomain(env, d, config.CertFiles{})
	case "remove":
		d := env.Paths.BaseDomains[0]
		ro := make([]huh.Option[string], 0, len(env.Paths.BaseDomains))
		for _, b := range env.Paths.BaseDomains {
			ro = append(ro, huh.NewOption(b, b))
		}
		if err := env.UI.Select("Remover qual?", &d, ro...); err != nil {
			return err
		}
		return env.P.RemoveBaseDomain(d)
	}
	return nil
}
