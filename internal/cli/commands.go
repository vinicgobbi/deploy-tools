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
	app := &cobra.Command{Use: "app", Short: "Criar, migrar, configurar e inspecionar apps"}

	var create provision.CreateOptions
	createCmd := &cobra.Command{
		Use:   "create <app>",
		Short: "Cria um app novo (estrutura + vhost)",
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
	createCmd.Flags().StringVarP(&create.Type, "type", "t", "", "laravel | react | static")
	createCmd.Flags().StringSliceVarP(&create.Domains, "domain", "d", nil,
		"domínio; repita ou separe por vírgula para vários, o 1º é o principal (padrão: <sub>.<domínio coringa>)")
	createCmd.Flags().StringVar(&create.Base, "base", "", "domínio coringa cadastrado a usar (server domain list)")
	createCmd.Flags().StringVar(&create.Sub, "sub", "", "subdomínio no --base (padrão: o nome do app)")
	createCmd.MarkFlagsMutuallyExclusive("domain", "base")
	certFlags(createCmd, &create.Cert)
	createCmd.Flags().BoolVar(&create.MigrateOnDeploy, "migrate-on-deploy", false, "laravel: roda php artisan migrate --force a cada deploy")
	createCmd.Flags().BoolVar(&create.Enable, "enable", false, "ativa o vhost e recarrega o Apache (com configtest)")
	_ = createCmd.RegisterFlagCompletionFunc("type", completeTypes)

	var mig provision.MigrateOptions
	var migYes bool
	migrateCmd := &cobra.Command{
		Use:   "migrate <app>",
		Short: "Converte um app já em produção para releases/current, sem downtime",
		Long: `Para apps que hoje servem direto de /var/www/<app> (diretório comum).
O conteúdo atual vira releases/<timestamp>-pre-migracao, .env e storage/
(Laravel) vão para shared/, e o vhost é repontado para current com
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
	migrateCmd.Flags().StringVarP(&mig.Type, "type", "t", "", "laravel | react | static")
	migrateCmd.Flags().StringVar(&mig.Vhost, "vhost", "", "vhost a editar (padrão: detectado em sites-enabled)")
	migrateCmd.Flags().BoolVar(&mig.MigrateOnDeploy, "migrate-on-deploy", false, "laravel: roda php artisan migrate --force a cada deploy")
	migrateCmd.Flags().BoolVarP(&migYes, "yes", "y", false, "não pede confirmação")
	_ = migrateCmd.RegisterFlagCompletionFunc("type", completeTypes)

	var enableYes bool
	enableCmd := &cobra.Command{
		Use:   "enable <app>",
		Short: "Coloca o site do app no ar (a2ensite, com configtest antes e depois)",
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
		Short: "Tira o site do app do ar (a2dissite, com configtest antes e depois)",
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
		Short:   "Lista os apps em /var/www",
		Args:    cobra.NoArgs,
		RunE:    func(*cobra.Command, []string) error { return doList(env) },
	}

	statusCmd := &cobra.Command{
		Use:               "status <app>",
		Short:             "Mostra config, vhost e releases de um app",
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
		Short: "Grava o .deploy.conf de um app existente (tipo, migrations)",
		Long: `Apps criados antes do deploy-tools em Go não têm .deploy.conf e fazem
deploy sem hooks (como sempre fizeram). Este comando grava a config;
para Laravel, os próximos deploys passam a rodar storage:link,
config:cache, route:cache e view:cache (e migrate --force, se ligado).
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
	configCmd.Flags().StringVarP(&cfgType, "type", "t", "", "laravel | react | static (padrão: detectar)")
	configCmd.Flags().BoolVar(&cfgMigrate, "migrate-on-deploy", false, "laravel: liga migrate --force no deploy")
	configCmd.Flags().BoolVar(&cfgNoMigrate, "no-migrate-on-deploy", false, "laravel: desliga migrate --force no deploy")
	configCmd.MarkFlagsMutuallyExclusive("migrate-on-deploy", "no-migrate-on-deploy")
	_ = configCmd.RegisterFlagCompletionFunc("type", completeTypes)

	app.AddCommand(createCmd, migrateCmd, enableCmd, disableCmd, listCmd, statusCmd, configCmd, domainCmd(env), certCmd(env))
	return app
}

func releaseCmd(env *Env) *cobra.Command {
	rel := &cobra.Command{Use: "release", Short: "Listar releases e fazer rollback"}

	listCmd := &cobra.Command{
		Use:               "list <app>",
		Aliases:           []string{"ls"},
		Short:             "Lista as releases (mais nova primeiro)",
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
		Short: "Volta current para outra release (padrão: a anterior)",
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
	key := &cobra.Command{Use: "key", Short: "Chaves SSH da CI (authorized_keys do usuário deploy)"}
	key.AddCommand(&cobra.Command{
		Use:   "add <arquivo.pub>",
		Short: "Autoriza uma chave pública, restrita ao forced command",
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
			env.UI.Println()
			printSecrets(env, env.P.Secrets(""), privateKeyName(args[0]), secretsTarget{})
			return nil
		},
	})
	key.AddCommand(&cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "Lista as chaves autorizadas",
		Args:    cobra.NoArgs,
		RunE:    func(*cobra.Command, []string) error { return doKeys(env) },
	})
	key.AddCommand(secretsCmd(env))
	return key
}

func serverCmd(env *Env) *cobra.Command {
	srv := &cobra.Command{Use: "server", Short: "Instalação e setup do servidor"}
	srv.AddCommand(&cobra.Command{
		Use:   "install",
		Short: "Instala/atualiza este binário (troca o deploy-ci atomicamente)",
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
	var key string
	setup := &cobra.Command{
		Use:   "setup",
		Short: "Setup de servidor novo: usuário deploy, binário e chave da CI",
		Long: `Cria (ou corrige) o usuário deploy, o ~/.ssh, instala o binário e,
com --key, autoriza a chave da CI. Idempotente: pode rodar de novo.`,
		Example: "  sudo ./deploy-tools server setup --key ci_deploy_key.pub",
		Args:    cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if err := env.RequireRoot(); err != nil {
				return err
			}
			if err := env.P.Setup(env.Self, key); err != nil {
				return err
			}
			env.UI.OK("Servidor pronto.")
			steps := []string{}
			if key == "" {
				steps = append(steps, "Autorize a chave da CI: sudo deploy-tools key add ci_deploy_key.pub")
			}
			steps = append(steps,
				"Configure os secrets no GitHub (veja abaixo; de novo a qualquer hora: deploy-tools key secrets).",
				"Cadastre os domínios coringa dos apps: sudo deploy-tools server domain add <domínio>.",
				"Crie os apps: sudo deploy-tools app create <app> --type laravel|react|static",
			)
			env.UI.Steps("Próximos passos", steps...)
			env.UI.Println()
			printSecrets(env, env.P.Secrets(""), privateKeyName(key), secretsTarget{})
			return nil
		},
	}
	setup.Flags().StringVar(&key, "key", "", "chave pública da CI a autorizar")
	srv.AddCommand(setup, certsCmd(env), statusCmd(env), baseDomainCmd(env))
	return srv
}
