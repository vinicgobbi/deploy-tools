package cli

import (
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/vinicgobbi/deploy-tools/internal/config"
	"github.com/vinicgobbi/deploy-tools/internal/provision"
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
se há um certificado *.<domínio> no servidor: com ele, todo app novo no
domínio já sai com HTTPS válido.`,
	}
	cmd.AddCommand(&cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "Lista os domínios coringa",
		Args:    cobra.NoArgs,
		RunE:    func(*cobra.Command, []string) error { return doBaseDomains(env) },
	})
	cmd.AddCommand(&cobra.Command{
		Use:     "add <domínio>...",
		Short:   "Cadastra um ou mais domínios coringa",
		Example: "  sudo deploy-tools server domain add vinicgobbi.dev.br\n  sudo deploy-tools server domain add '*.example.com'",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := env.RequireRoot(); err != nil {
				return err
			}
			defer syncPaths(env)
			for _, d := range args {
				if err := env.P.AddBaseDomain(d); err != nil {
					return err
				}
			}
			return nil
		},
	})
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
		return env.P.AddBaseDomain(d)
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
