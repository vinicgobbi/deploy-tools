package cli

import (
	"github.com/spf13/cobra"

	"github.com/vinicgobbi/deploy-tools/internal/provision"
	"github.com/vinicgobbi/deploy-tools/internal/validate"
)

// appArg returns args[0], or prompts for it on a TTY.
func appArg(env *Env, args []string, example string) (string, error) {
	if len(args) > 0 {
		return args[0], validate.AppName(args[0])
	}
	if !env.UI.Interactive {
		return "", missing("nome do app", example)
	}
	var app string
	err := env.UI.Input("Nome do app", "ex.: minha-api", &app, validate.AppName)
	return app, err
}

// typeFlag returns the --type value, or prompts for it on a TTY.
func typeFlag(env *Env, typ, example string) (string, error) {
	if typ != "" {
		return typ, validate.Type(typ)
	}
	if !env.UI.Interactive {
		return "", missing("--type", example)
	}
	err := env.UI.Select("Tipo do app", &typ, typeOptions()...)
	return typ, err
}

func completeTypes(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return validate.Types, cobra.ShellCompDirectiveNoFileComp
}

func completeApps(env *Env) cobra.CompletionFunc {
	return func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		apps, _ := env.P.Apps()
		var names []string
		for _, a := range apps {
			names = append(names, a.Name)
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	}
}

func appCmd(env *Env) *cobra.Command {
	app := &cobra.Command{Use: "app", Short: "Criar, ver e publicar sites: domínios, HTTPS, no ar/fora do ar"}

	var create provision.CreateOptions
	createCmd := &cobra.Command{
		Use:   "create <app>",
		Short: "Cria um app novo (pasta, configuração e site no Apache)",
		Long: `Cria /var/www/<app>/{releases,shared}, o .deploy.conf e o vhost em
sites-available. Sem --enable, o vhost não é ativado: revise DNS e
certificado e depois rode "deploy-tools app enable <app>".

Sem --domain, o app ganha <subdomínio>.<domínio coringa> (cadastrados com
"deploy-tools server domain add"): com um só, ele é usado direto; com
vários, o comando pergunta qual (ou use --base/--sub).

O certificado é procurado no servidor (Let's Encrypt, /etc/ssl, /etc/pki/tls,
pastas do Apache e SSL_SEARCH_DIRS de /etc/deploy-tools.conf): vale o que
cobrir todos os domínios. Sem nenhum (num terminal, o comando oferece
informar um), o site fica só em HTTP até você rodar "deploy-tools app cert <app>".`,
		Example: `  sudo deploy-tools app create minha-api --type laravel
  sudo deploy-tools app create meu-front --type react --domain app.example.com --domain www.app.example.com --enable
  sudo deploy-tools app create docs --type static --base vinicgobbi.dev.br            # docs.vinicgobbi.dev.br
  sudo deploy-tools app create painel --type react --base vinicgobbi.dev.br --sub admin`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) (err error) {
			if err := env.RequireRoot(); err != nil {
				return err
			}
			const ex = "deploy-tools app create minha-api --type laravel"
			if create.App, err = appArg(env, args, ex); err != nil {
				return err
			}
			if create.Type, err = typeFlag(env, create.Type, ex); err != nil {
				return err
			}
			// No domain given and no single base to default to: ask.
			if env.UI.Interactive && len(create.Domains) == 0 && create.Base == "" && len(env.Paths.BaseDomains) != 1 {
				if create.Domains, err = chooseDomains(env, create.App, true); err != nil {
					return err
				}
			}
			if err := askCertForApp(env, &create); err != nil {
				return err
			}
			return doCreate(env, create)
		},
	}
	createCmd.Flags().StringVarP(&create.Type, "type", "t", "", "laravel | symfony | react | angular | static")
	createCmd.Flags().StringSliceVarP(&create.Domains, "domain", "d", nil,
		"domínio; repita ou separe por vírgula para vários, o 1º é o principal (padrão: <sub>.<domínio coringa>)")
	createCmd.Flags().StringVar(&create.Base, "base", "", "domínio coringa cadastrado a usar (server domain list)")
	createCmd.Flags().StringVar(&create.Sub, "sub", "", "subdomínio no --base (padrão: o nome do app)")
	createCmd.MarkFlagsMutuallyExclusive("domain", "base")
	certFlags(createCmd, &create.Cert)
	createCmd.Flags().BoolVar(&create.MigrateOnDeploy, "migrate-on-deploy", false, "laravel/symfony: roda as migrations a cada deploy")
	createCmd.Flags().BoolVar(&create.Enable, "enable", false, "ativa o vhost e recarrega o Apache (com configtest)")
	_ = createCmd.RegisterFlagCompletionFunc("type", completeTypes)

	var mig provision.MigrateOptions
	var migYes bool
	migrateCmd := &cobra.Command{
		Use:   "migrate <app>",
		Short: "Converte um site que já está no ar para o deploy por versões, sem derrubá-lo",
		Long: `Para apps que hoje servem direto de /var/www/<app> (diretório comum).
O conteúdo atual vira releases/<timestamp>-pre-migracao, .env e storage/
(Laravel), ou .env.local e var/log (Symfony), vão para shared/, e o vhost é repontado para current com
configtest antes do reload. O site continua no ar o tempo todo.`,
		Example: `  sudo deploy-tools app migrate docs --type static
  sudo deploy-tools app migrate sistema --type laravel --vhost /etc/apache2/sites-available/sistema-ssl.conf`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeApps(env),
		RunE: func(_ *cobra.Command, args []string) (err error) {
			if err := env.RequireRoot(); err != nil {
				return err
			}
			const ex = "deploy-tools app migrate docs --type static"
			if mig.App, err = appArg(env, args, ex); err != nil {
				return err
			}
			if mig.Type, err = typeFlag(env, mig.Type, ex); err != nil {
				return err
			}
			return doMigrate(env, mig, migYes)
		},
	}
	migrateCmd.Flags().StringVarP(&mig.Type, "type", "t", "", "laravel | symfony | react | angular | static")
	migrateCmd.Flags().StringVar(&mig.Vhost, "vhost", "", "vhost a editar (padrão: detectado em sites-enabled)")
	migrateCmd.Flags().BoolVar(&mig.MigrateOnDeploy, "migrate-on-deploy", false, "laravel/symfony: roda as migrations a cada deploy")
	migrateCmd.Flags().BoolVarP(&migYes, "yes", "y", false, "não pede confirmação")
	_ = migrateCmd.RegisterFlagCompletionFunc("type", completeTypes)

	var enableYes bool
	enableCmd := &cobra.Command{
		Use:   "enable <app>",
		Short: "Coloca o site no ar",
		Long: `Ativa o site do app com a2ensite. A config do Apache precisa passar no
configtest antes (se já estiver com erro, nada é alterado) e depois da
mudança; só então o Apache é recarregado. Se o teste ou o reload falhar,
o site é desativado de novo.`,
		Example:           "  sudo deploy-tools app enable minha-api",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeApps(env),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := env.RequireRoot(); err != nil {
				return err
			}
			a, err := appArg(env, args, "deploy-tools app enable minha-api")
			if err != nil {
				return err
			}
			return doEnable(env, a, enableYes)
		},
	}
	enableCmd.Flags().BoolVarP(&enableYes, "yes", "y", false, "não pede confirmação")

	var disableYes bool
	disableCmd := &cobra.Command{
		Use:   "disable <app>",
		Short: "Tira o site do ar (arquivos e versões continuam)",
		Long: `Desativa o site do app com a2dissite, com as mesmas checagens do enable:
configtest antes e depois, reload só se passar e, em caso de falha, o site
é ativado de novo. Arquivos, releases e deploy pela CI não mudam; volte com
"deploy-tools app enable <app>".`,
		Example:           "  sudo deploy-tools app disable minha-api",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeApps(env),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := env.RequireRoot(); err != nil {
				return err
			}
			a, err := appArg(env, args, "deploy-tools app disable minha-api")
			if err != nil {
				return err
			}
			return doDisable(env, a, disableYes)
		},
	}
	disableCmd.Flags().BoolVarP(&disableYes, "yes", "y", false, "não pede confirmação")

	listCmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "Lista os apps do servidor e a situação de cada um",
		Args:    cobra.NoArgs,
		RunE:    func(*cobra.Command, []string) error { return doList(env) },
	}

	statusCmd := &cobra.Command{
		Use:               "status <app>",
		Short:             "Mostra tudo sobre um app: endereço, versões e o que falta",
		Example:           "  deploy-tools app status docs",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeApps(env),
		RunE: func(_ *cobra.Command, args []string) error {
			a, err := appArg(env, args, "deploy-tools app status docs")
			if err != nil {
				return err
			}
			return doStatus(env, a)
		},
	}

	var cfgType string
	var cfgMigrate, cfgNoMigrate bool
	configCmd := &cobra.Command{
		Use:   "config <app>",
		Short: "Muda o tipo do app e se o deploy roda as migrations",
		Long: `Apps criados antes do deploy-tools em Go não têm .deploy.conf e fazem
deploy sem hooks (como sempre fizeram). Este comando grava a config;
para Laravel, os próximos deploys passam a rodar storage:link,
config:cache, route:cache e view:cache (e migrate --force, se ligado); para
Symfony, cache:clear (e doctrine:migrations:migrate, se ligado).
Sem --type, o tipo é detectado a partir de current/.`,
		Example: `  sudo deploy-tools app config sistema --type laravel --migrate-on-deploy
  sudo deploy-tools app config docs`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeApps(env),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := env.RequireRoot(); err != nil {
				return err
			}
			a, err := appArg(env, args, "deploy-tools app config docs --type static")
			if err != nil {
				return err
			}
			var migrate *bool
			if cmd.Flags().Changed("migrate-on-deploy") {
				migrate = &cfgMigrate
			}
			if cmd.Flags().Changed("no-migrate-on-deploy") {
				f := !cfgNoMigrate
				migrate = &f
			}
			return doConfig(env, a, cfgType, migrate)
		},
	}
	configCmd.Flags().StringVarP(&cfgType, "type", "t", "", "laravel | symfony | react | angular | static (padrão: detectar)")
	configCmd.Flags().BoolVar(&cfgMigrate, "migrate-on-deploy", false, "laravel/symfony: liga as migrations no deploy")
	configCmd.Flags().BoolVar(&cfgNoMigrate, "no-migrate-on-deploy", false, "laravel/symfony: desliga as migrations no deploy")
	configCmd.MarkFlagsMutuallyExclusive("migrate-on-deploy", "no-migrate-on-deploy")
	_ = configCmd.RegisterFlagCompletionFunc("type", completeTypes)

	app.AddGroup(
		&cobra.Group{ID: "start", Title: "Começar:"},
		&cobra.Group{ID: "site", Title: "Site:"},
		&cobra.Group{ID: "adjust", Title: "Ajustes:"},
	)
	grouped(app, "start", createCmd, listCmd, statusCmd)
	grouped(app, "site", enableCmd, disableCmd, domainCmd(env), certCmd(env))
	grouped(app, "adjust", configCmd, migrateCmd)
	return app
}

func releaseCmd(env *Env) *cobra.Command {
	rel := &cobra.Command{Use: "release", Short: "Versões publicadas de cada app e volta para uma anterior"}

	listCmd := &cobra.Command{
		Use:               "list <app>",
		Aliases:           []string{"ls"},
		Short:             "Lista as versões publicadas de um app (mais nova primeiro)",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeApps(env),
		RunE: func(_ *cobra.Command, args []string) error {
			a, err := appArg(env, args, "deploy-tools release list docs")
			if err != nil {
				return err
			}
			rels, err := env.P.Releases().List(a)
			if err != nil {
				return hint(err, a)
			}
			printReleases(env, rels, true)
			return nil
		},
	}

	var yes bool
	rollbackCmd := &cobra.Command{
		Use:   "rollback <app> [release]",
		Short: "Volta o app para uma versão anterior, na hora (padrão: a anterior)",
		Long: `Repõe current para uma release que ainda está em disco, sem novo build.
Sem [release], volta para a release imediatamente anterior à atual.`,
		Example: `  sudo deploy-tools release rollback docs
  sudo deploy-tools release rollback docs 20260910120000-db4e609`,
		Args: cobra.MaximumNArgs(2),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, s string) ([]string, cobra.ShellCompDirective) {
			if len(args) == 0 {
				return completeApps(env)(cmd, args, s)
			}
			rels, _ := env.P.Releases().List(args[0])
			var ids []string
			for _, r := range rels {
				ids = append(ids, r.ID)
			}
			return ids, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(_ *cobra.Command, args []string) error {
			if err := env.RequireRoot(); err != nil {
				return err
			}
			a, err := appArg(env, args, "deploy-tools release rollback docs")
			if err != nil {
				return err
			}
			id := ""
			if len(args) > 1 {
				id = args[1]
			}
			return doRollback(env, a, id, yes)
		},
	}
	rollbackCmd.Flags().BoolVarP(&yes, "yes", "y", false, "não pede confirmação")

	rel.AddCommand(listCmd, rollbackCmd)
	return rel
}

func keyCmd(env *Env) *cobra.Command {
	key := &cobra.Command{Use: "key", Short: "Chave e secrets que o GitHub usa para publicar"}
	key.AddCommand(&cobra.Command{
		Use:   "add <arquivo.pub>",
		Short: "Autoriza uma chave pública criada na sua máquina",
		Long: `Adiciona a chave ao authorized_keys do usuário deploy com
command="/usr/local/bin/deploy-ci" e no-pty etc., para que ela só
consiga fazer deploy/rollback/list — nunca abrir um shell.
Gere o par na sua máquina (não no servidor):
  ssh-keygen -t ed25519 -C "ci-deploy" -f ci_deploy_key -N ""`,
		Example: "  sudo deploy-tools key add ci_deploy_key.pub",
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := env.RequireRoot(); err != nil {
				return err
			}
			if err := env.P.AddKey(args[0]); err != nil {
				return err
			}
			if err := ensureDeployHost(env, false); err != nil {
				return err
			}
			env.UI.Println()
			printSecrets(env, env.P.Secrets(""), privateKeyName(args[0]), secretsTarget{})
			return nil
		},
	})
	key.AddCommand(&cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "Lista as chaves que podem publicar",
		Args:    cobra.NoArgs,
		RunE:    func(*cobra.Command, []string) error { return doKeys(env) },
	})
	key.AddCommand(generateKeyCmd(env))
	key.AddCommand(secretsCmd(env))
	order(key, "generate", "add", "list", "secrets")
	return key
}

func serverCmd(env *Env) *cobra.Command {
	srv := &cobra.Command{Use: "server", Short: "Preparar e configurar o servidor"}
	srv.AddCommand(&cobra.Command{
		Use:   "install",
		Short: "Instala esta cópia do deploy-tools no servidor",
		Long: `Copia este binário para /usr/local/bin/deploy-tools e faz de
/usr/local/bin/deploy-ci um symlink para ele. Não mexe em usuário,
chaves nem apps: é o comando para atualizar um servidor já configurado.
Se deploy-ci já existia e não era um symlink, ele fica em
/usr/local/bin/deploy-ci.bak.`,
		Example: `  scp bin/deploy-tools servidor:/tmp/
  ssh servidor sudo /tmp/deploy-tools server install`,
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if err := env.RequireRoot(); err != nil {
				return err
			}
			if err := env.P.Install(env.Self); err != nil {
				return err
			}
			env.install = nil
			env.UI.OK("Instalado. Os próximos deploys da CI já usam a versão nova.")
			return nil
		},
	})
	var key, keyOut, host string
	var genKey bool
	setup := &cobra.Command{
		Use:   "setup",
		Short: "Prepara um servidor novo: usuário de deploy, instalação e chave do GitHub",
		Long: `Cria (ou corrige) o usuário deploy, o ~/.ssh, instala o binário e,
com --key, autoriza a chave da CI; com --generate-key, gera a chave aqui
mesmo e grava o par em ./ci_deploy_key(.pub), na pasta atual. Idempotente:
pode rodar de novo.`,
		Example: `  sudo ./deploy-tools server setup --generate-key
  sudo ./deploy-tools server setup --key ci_deploy_key.pub`,
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if err := env.RequireRoot(); err != nil {
				return err
			}
			if err := env.P.Setup(env.Self, key); err != nil {
				return err
			}
			if host != "" {
				if err := env.P.SetDeployHost(host); err != nil {
					return err
				}
				syncPaths(env)
			} else if err := ensureDeployHost(env, true); err != nil {
				return err
			}
			env.install = nil
			env.UI.OK("Servidor pronto.")
			if genKey {
				return doGenerateKey(env, keyGen{out: keyOut})
			}
			steps := []string{}
			if key == "" {
				steps = append(steps, "Autorize a chave da CI: sudo deploy-tools key add ci_deploy_key.pub "+
					"(ou gere aqui: sudo deploy-tools key generate)")
			}
			steps = append(steps,
				"Configure os secrets no GitHub (veja abaixo; de novo a qualquer hora: deploy-tools key secrets).",
				"Cadastre os domínios coringa dos apps: sudo deploy-tools server domain add <domínio>.",
				"Crie os apps: sudo deploy-tools app create <app> --type laravel|symfony|react|angular|static",
			)
			env.UI.Steps("Próximos passos", steps...)
			env.UI.Println()
			printSecrets(env, env.P.Secrets(""), privateKeyName(key), secretsTarget{})
			return nil
		},
	}
	setup.Flags().StringVar(&key, "key", "", "chave pública da CI a autorizar")
	setup.Flags().StringVar(&host, "host", "", "endereço pelo qual a CI alcança o servidor (hostname ou IP; vira DEPLOY_HOST)")
	setup.Flags().BoolVar(&genKey, "generate-key", false, "gera a chave da CI aqui e grava em ./ci_deploy_key (veja key generate)")
	setup.Flags().StringVar(&keyOut, "key-out", "", "com --generate-key: grava a chave privada neste arquivo (padrão: ./ci_deploy_key)")
	setup.MarkFlagsMutuallyExclusive("key", "generate-key")
	srv.AddCommand(setup, certsCmd(env), statusCmd(env), baseDomainCmd(env))
	order(srv, "setup", "status", "domain", "certs", "install")
	return srv
}

// grouped adds cmds to parent under group id, in this order.
func grouped(parent *cobra.Command, id string, cmds ...*cobra.Command) {
	for _, c := range cmds {
		c.GroupID = id
		parent.AddCommand(c)
	}
}

// order re-adds parent's subcommands with names first, in that order (help
// lists them as added; see clitext.Localize).
func order(parent *cobra.Command, names ...string) {
	cmds := parent.Commands()
	parent.RemoveCommand(cmds...)
	byName := map[string]*cobra.Command{}
	for _, c := range cmds {
		byName[c.Name()] = c
	}
	for _, n := range names {
		if c, ok := byName[n]; ok {
			parent.AddCommand(c)
			delete(byName, n)
		}
	}
	for _, c := range cmds {
		if _, ok := byName[c.Name()]; ok {
			parent.AddCommand(c)
		}
	}
}
