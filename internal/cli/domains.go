package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/vinicgobbi/deploy-tools/internal/config"
	"github.com/vinicgobbi/deploy-tools/internal/provision"
	"github.com/vinicgobbi/deploy-tools/internal/tlscert"
)

// certFlags registers --cert/--key/--chain on cmd.
func certFlags(cmd *cobra.Command, c *provision.CertChoice) {
	cmd.Flags().StringVar(&c.File, "cert", "", "certificado (padrão: procurado no servidor)")
	cmd.Flags().StringVar(&c.Key, "key", "", "chave privada do --cert")
	cmd.Flags().StringVar(&c.Chain, "chain", "", "cadeia/CA bundle separado (opcional)")
	cmd.MarkFlagsRequiredTogether("cert", "key")
}

func domainCmd(env *Env) *cobra.Command {
	dom := &cobra.Command{
		Use:   "domain",
		Short: "Domínios do site de um app (listar, adicionar, remover)",
		Long: `O primeiro domínio é o ServerName do site; os outros são ServerAlias.
Todo domínio cadastrado ganha também o alias www.<domínio> (--no-www evita).
Ao adicionar ou remover, se o certificado atual não cobrir o novo conjunto,
o deploy-tools procura um que cubra e troca. Um site ativo é alterado com
configtest antes e depois, e restaurado se algo falhar.`,
	}
	dom.AddCommand(&cobra.Command{
		Use:               "list <app>",
		Aliases:           []string{"ls"},
		Short:             "Lista os domínios do site",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeApps(env),
		RunE: func(_ *cobra.Command, args []string) error {
			a, err := appArg(env, args, "deploy-tools app domain list minha-api")
			if err != nil {
				return err
			}
			ds, err := env.P.Domains(a)
			if err != nil {
				return err
			}
			for i, d := range ds {
				if i == 0 {
					env.UI.Println(d + "  " + env.UI.Faint("(principal)"))
				} else {
					env.UI.Println(d)
				}
			}
			return nil
		},
	})
	var noWWW bool
	add := &cobra.Command{
		Use:               "add <app> <domínio>...",
		Short:             "Adiciona um ou mais domínios ao site (cada um com o alias www.)",
		Example:           "  sudo deploy-tools app domain add minha-api api.example.com      # + www.api.example.com",
		Args:              cobra.MinimumNArgs(2),
		ValidArgsFunction: completeApps(env),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := env.RequireRoot(); err != nil {
				return err
			}
			if err := env.P.AddDomains(args[0], args[1:], !noWWW); err != nil {
				return err
			}
			env.UI.OK("Domínios atualizados.")
			env.UI.Hint("Confirme que o DNS dos domínios novos (e do www.) aponta para este servidor.")
			return nil
		},
	}
	add.Flags().BoolVar(&noWWW, "no-www", false, "não adiciona o alias www.<domínio>")
	dom.AddCommand(add)
	dom.AddCommand(&cobra.Command{
		Use:               "remove <app> <domínio>...",
		Aliases:           []string{"rm"},
		Short:             "Remove um ou mais domínios do site",
		Long:              "Remove também o www.<domínio>. Remover o domínio principal promove o seguinte. O último domínio não pode ser removido.",
		Example:           "  sudo deploy-tools app domain remove minha-api antigo.example.com",
		Args:              cobra.MinimumNArgs(2),
		ValidArgsFunction: completeApps(env),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := env.RequireRoot(); err != nil {
				return err
			}
			if err := env.P.RemoveDomains(args[0], args[1:]); err != nil {
				return err
			}
			env.UI.OK("Domínios atualizados.")
			return nil
		},
	})
	return dom
}

func certCmd(env *Env) *cobra.Command {
	var c provision.CertChoice
	cmd := &cobra.Command{
		Use:   "cert <app>",
		Short: "Troca o certificado TLS do site (procura um que cubra os domínios)",
		Long: `Sem flags, procura no servidor um certificado válido que cubra todos os
domínios do site e tenha a chave privada correspondente:
  ` + strings.Join(tlscert.DefaultDirs, "\n  ") + `
  + SSL_SEARCH_DIRS de ` + config.File + `
Com SSL_CERT_FILE em ` + config.File + `, usa sempre aquele.
Com --cert/--key, usa os arquivos informados.

Use depois de emitir ou renovar um certificado (ex.: certbot certonly).
Nunca troca um certificado real pelo autoassinado provisório.`,
		Example: `  sudo deploy-tools app cert minha-api
  sudo deploy-tools app cert minha-api --cert /etc/ssl/certs/x.crt --key /etc/ssl/private/x.key`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeApps(env),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := env.RequireRoot(); err != nil {
				return err
			}
			a, err := appArg(env, args, "deploy-tools app cert minha-api")
			if err != nil {
				return err
			}
			got, err := env.P.UpdateCert(a, c)
			if err != nil {
				return err
			}
			if got.Fallback {
				env.UI.Warn("Nenhum certificado no servidor cobre os domínios; o site segue como estava (sem HTTPS, se não tinha).")
				env.UI.Hint("Informe os arquivos: sudo deploy-tools app cert %s --cert <arquivo> --key <arquivo>", a)
				return nil
			}
			env.UI.OK("Certificado do site: %s", got.File)
			return nil
		},
	}
	certFlags(cmd, &c)
	return cmd
}

func certsCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "certs",
		Short: "Lista os certificados TLS encontrados no servidor",
		Long: `Mostra os certificados (com chave privada correspondente) que o
deploy-tools encontra e pode usar nos sites, com os domínios cobertos e a
validade. As pastas privadas só são legíveis como root.`,
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if env.Paths.CertFile != "" {
				env.UI.Println("Certificado fixo em " + config.File + ": " + env.Paths.CertFile)
				env.UI.Println()
			}
			certs := tlscert.Scan(env.Paths.CertDirs)
			if len(certs) == 0 {
				env.UI.Println("Nenhum certificado com chave encontrado em:")
				for _, d := range env.Paths.CertDirs {
					env.UI.Println("  " + d)
				}
				if err := env.RequireRoot(); err != nil {
					env.UI.Hint("Rode com sudo: as chaves privadas só são legíveis como root.")
				}
				return nil
			}
			now := env.now()
			rows := [][]string{{"DOMÍNIOS", "VÁLIDO ATÉ", "ARQUIVO"}}
			for _, c := range certs {
				until := c.NotAfter.Format("02/01/2006")
				switch {
				case now.After(c.NotAfter):
					until += " (expirado)"
				case c.SelfSign:
					until += " (autoassinado)"
				}
				rows = append(rows, []string{strings.Join(c.Names, " "), until, c.String()})
			}
			printTable(env, rows)
			return nil
		},
	}
}
