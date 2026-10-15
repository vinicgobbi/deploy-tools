package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/vinicgobbi/deploy-tools/internal/apache"
	"github.com/vinicgobbi/deploy-tools/internal/appconf"
	"github.com/vinicgobbi/deploy-tools/internal/config"
	"github.com/vinicgobbi/deploy-tools/internal/provision"
	"github.com/vinicgobbi/deploy-tools/internal/release"
	"github.com/vinicgobbi/deploy-tools/internal/tui"
	"github.com/vinicgobbi/deploy-tools/internal/ui"
	"github.com/vinicgobbi/deploy-tools/internal/validate"
)

// interactive is `deploy-tools` with no arguments: the full-screen TUI
// (a line-based menu in accessible mode). Each item only collects options
// and calls the same do* action the flag-driven commands use.
func interactive(env *Env) error {
	env.TUI = true
	defer func() { env.TUI = false }()
	return tui.Run(env.UI, tuiApp(env), &env.P.Out)
}

func tuiApp(env *Env) tui.App {
	isRoot := func() bool { return env.RequireRoot() == nil }
	// rooted fails before any question, not after the whole form.
	rooted := func(f func() error) func() error {
		return func() error {
			if err := env.RequireRoot(); err != nil {
				return err
			}
			return f()
		}
	}
	text := func(s string) func() string { return func() string { return s } }
	apps := func(title string, filter func(provision.AppInfo) bool, empty string) string {
		list, err := listApps(env)
		if err != nil {
			return ""
		}
		var names []string
		for _, a := range list {
			if filter(a) {
				names = append(names, a.Name)
			}
		}
		if len(names) == 0 {
			return empty
		}
		return title + " " + strings.Join(names, ", ") + "."
	}

	app := tui.App{
		Title: "deploy-tools " + env.Version,
		Subtitle: func() string {
			host, _ := os.Hostname()
			if env.UpdateNote != "" {
				host += " · " + env.UpdateNote
			}
			if _, loose := installSummary(env); loose {
				return host + " · binário solto (" + env.installState().Self + ")"
			}
			return host
		},
		IsRoot: isRoot,
		Notice: func() string { return nextStep(env) },
		Sections: []tui.Section{
			{Title: "Apps", Items: []tui.Item{
				{
					Label: "Criar app", Root: true, Run: rooted(func() error { return iCreate(env) }),
					Desc: text("Cria a pasta do app em " + env.Paths.AppsRoot + ", o .deploy.conf e o site no Apache " +
						"(HTTPS, com o HTTP redirecionando).\n\nVocê escolhe o nome, o tipo (Laravel, Symfony, React, Angular ou estático), " +
						"o domínio (um subdomínio de um domínio coringa, ou qualquer outro) e se o site já entra no ar. Nada é criado antes do resumo final."),
					CLI: "sudo deploy-tools app create <app> --type laravel|symfony|react|angular|static",
				},
				{
					Label: "Listar apps", Run: func() error { return doList(env) },
					Desc: func() string { return tui.Capture(env.UI, func() { _ = doList(env) }) },
					CLI:  "deploy-tools app list",
				},
				{
					Label: "Ver um app", Run: func() error { return iStatus(env) },
					Desc: text("Mostra o endereço, o tipo, a release no ar, as releases guardadas e o que falta " +
						"para o app ficar no ar."),
					CLI: "deploy-tools app status <app>",
				},
				{
					Label: "Colocar no ar", Root: true, Run: rooted(func() error { return iEnable(env) }),
					Desc: func() string {
						return "Coloca no ar o site de um app (a2ensite). A config do Apache é testada antes de mexer " +
							"e de novo depois; o Apache só recarrega se passar, senão o site é desativado de novo.\n\n" +
							"Confira antes o DNS e o certificado.\n\n" +
							apps("Sites desativados:", func(a provision.AppInfo) bool { return a.Vhost != "" && !a.VhostEnabled }, "Nenhum site desativado.")
					},
					CLI: "sudo deploy-tools app enable <app>",
				},
				{
					Label: "Tirar do ar", Root: true, Run: rooted(func() error { return iDisable(env) }),
					Desc: func() string {
						return "Tira do ar o site de um app (a2dissite), com as mesmas checagens do Ativar. " +
							"Os arquivos, as releases e o deploy pela CI continuam; para voltar, use Colocar no ar.\n\n" +
							apps("Sites ativos:", func(a provision.AppInfo) bool { return a.Vhost != "" && a.VhostEnabled }, "Nenhum site ativo.")
					},
					CLI: "sudo deploy-tools app disable <app>",
				},
				{
					Label: "Domínios do site", Root: true, Run: rooted(func() error { return iDomains(env) }),
					Desc: text("Adiciona ou remove domínios do site de um app. O primeiro é o principal (ServerName); " +
						"os outros viram ServerAlias.\n\nSe o certificado atual não cobrir os domínios novos, procura " +
						"um que cubra e troca. Um site ativo é testado antes e depois, e restaurado se algo falhar."),
					CLI: "sudo deploy-tools app domain add|remove <app> <domínio>...",
				},
				{
					Label: "HTTPS do site", Root: true, Run: rooted(func() error { return iCert(env) }),
					Desc: func() string {
						return "Procura no servidor um certificado válido que cubra todos os domínios do site, com a " +
							"chave correspondente, e troca no site. Use depois de emitir ou renovar (ex.: certbot).\n\n" +
							"Procura em: " + strings.Join(env.Paths.CertDirs, ", ") + ".\n\n" +
							apps("Sem HTTPS de verdade:", func(a provision.AppInfo) bool {
								return a.Vhost != "" && (a.CertFile == "" || a.CertFile == config.FallbackCert)
							}, "Todos os sites têm certificado.")
					},
					CLI: "sudo deploy-tools app cert <app> [--cert arquivo --key arquivo]",
				},
				{
					Label: "Voltar versão", Root: true, Run: rooted(func() error { return iRollback(env) }),
					Desc: text("Volta um app para uma release anterior que ainda está em disco. Não refaz o build: " +
						"só troca a release ativa, na hora.\n\nO próximo push publica uma release nova normalmente."),
					CLI: "sudo deploy-tools release rollback <app> [release]",
				},
				{
					Label: "Tipo e migrations", Root: true, Run: rooted(func() error { return iConfig(env) }),
					Desc: func() string {
						return "Grava o .deploy.conf dos apps já migrados que ainda não têm: o tipo e, no Laravel e no Symfony, " +
							"se o deploy roda as migrations. Com ele, o deploy passa a rodar os hooks do tipo " +
							"(storage:link e caches no Laravel, cache:clear no Symfony).\n\n" +
							apps("Sem .deploy.conf:", needsConf, "Todos os apps migrados já têm .deploy.conf. "+
								"Para mudar a config de um, use o comando abaixo.")
					},
					CLI: "sudo deploy-tools app config <app> [--type t] [--migrate-on-deploy]",
				},
				{
					Label: "Converter site existente", Root: true, Run: rooted(func() error { return iMigrate(env) }),
					Desc: func() string {
						return "Converte um app que serve direto de " + env.Paths.AppsRoot + "/<app> para o modelo de " +
							"releases, sem tirar o site do ar: o conteúdo atual vira a primeira release e o site do " +
							"Apache passa a apontar para ela.\n\n" +
							apps("Precisam migrar:", needsMigration, "Nenhum app precisa migrar.")
					},
					CLI: "sudo deploy-tools app migrate <app> --type ...",
				},
			}},
			{Title: "Servidor", Items: []tui.Item{
				{
					Label: "Preparar servidor", Root: true, Run: rooted(func() error { return iSetup(env) }),
					Desc: text("Prepara um servidor novo: cria (ou corrige) o usuário " + env.Paths.DeployUser +
						", instala o binário e cuida da chave da CI: gera aqui na hora (gravada em ./ci_deploy_key), " +
						"autoriza um .pub seu ou pula. Pode rodar de novo sem problema."),
					CLI: "sudo deploy-tools server setup [--generate-key | --key arquivo.pub]",
				},
				{
					Label: "Chaves do GitHub", Run: func() error { return doKeys(env) },
					Desc: func() string {
						return "Chaves SSH autorizadas para o usuário " + env.Paths.DeployUser + ". Cada uma só pode " +
							"rodar o deploy-ci, nunca abrir um shell.\n\n" + tui.Capture(env.UI, func() { _ = doKeys(env) })
					},
					CLI: "sudo deploy-tools key list",
				},
				{
					Label: "Gerar ou adicionar chave", Root: true, Run: rooted(func() error { return iAddKey(env) }),
					Desc: text("Autoriza uma chave da CI, restrita ao comando de deploy. Duas opções:\n\n" +
						"• Gerar agora, aqui: a pública é autorizada e o par é gravado em ci_deploy_key(.pub), na pasta " +
						"de onde você abriu o deploy-tools, para o secret DEPLOY_SSH_KEY. Apague depois de cadastrar.\n" +
						"• Usar um .pub gerado na sua máquina:\n" +
						"ssh-keygen -t ed25519 -C ci-deploy -f ci_deploy_key -N \"\""),
					CLI: "sudo deploy-tools key generate  |  sudo deploy-tools key add <arquivo.pub>",
				},
				{
					Label: "Secrets do GitHub", Run: func() error {
						if err := ensureDeployHost(env, false); err != nil {
							return err
						}
						s := env.P.Secrets("")
						printSecrets(env, s, "", secretsTarget{})
						return offerCopy(env, ghCommands(s, "", secretsTarget{}), ghScript(s, ""))
					},
					Desc: text("Mostra de novo como devem ficar os secrets dos repositórios no GitHub " +
						"(DEPLOY_HOST, DEPLOY_USER, DEPLOY_SSH_KEY, DEPLOY_HOST_KEY), com os valores deste servidor " +
						"e os comandos gh equivalentes.\n\n⚠ " + secretsWarning),
					CLI: "deploy-tools key secrets [--host h] [--repo dono/repo | --org org]",
				},
				{
					Label: "Domínios coringa", Root: true, Run: rooted(func() error { return iBaseDomains(env) }),
					Desc: func() string {
						return "Domínios base, como vinicgobbi.dev.br: ao criar um app, você escolhe um deles e o app " +
							"ganha <subdomínio>.<domínio>. Com um certificado *.<domínio> no servidor, todo app novo " +
							"já sai com HTTPS válido.\n\n" + tui.Capture(env.UI, func() { _ = doBaseDomains(env) })
					},
					CLI: "sudo deploy-tools server domain add|remove|list <domínio>",
				},
				{
					Label: "Certificados HTTPS", Run: func() error { return certsCmd(env).RunE(nil, nil) },
					Desc: text("Lista os certificados que o deploy-tools encontra no servidor e pode usar nos sites, " +
						"com os domínios e a validade.\n\nPara fixar um certificado ou procurar em outras pastas, " +
						"configure " + config.File + " (SSL_CERT_FILE, SSL_CERT_KEY, SSL_SEARCH_DIRS)."),
					CLI: "deploy-tools server certs",
				},
			}},
		},
	}
	app.Sections = append(app.Sections, tui.Section{Title: "Esta ferramenta", Items: []tui.Item{{
		Label: "Verificar atualizações", Run: func() error { return iCheckUpdate(env) },
		Desc: func() string {
			return "Consulta a última release no GitHub e, se houver versão nova, oferece instalar " +
				"(com sudo). O download é conferido pelo SHA-256 publicado na release.\n\n" +
				"Esta versão: " + env.Version + ".\n" + autoUpdateState(env)
		},
		CLI: "sudo deploy-tools update  |  deploy-tools update --check",
	}}})
	// Called by name, it is the installed binary (kept up to date by the
	// automatic update): installing makes sense only for a loose copy.
	if !env.FromPATH {
		srv := &app.Sections[len(app.Sections)-2] // Servidor
		install := tui.Item{
			Label: "Instalar/atualizar", Root: true, Run: rooted(func() error { return iInstall(env) }),
			Desc: func() string {
				lines, _ := installSummary(env)
				return "Instala este binário (" + env.Version + ") em " + env.Paths.BinDir + " e troca o " +
					"deploy-ci de forma atômica. Não mexe em usuário, chaves nem apps: os próximos " +
					"deploys da CI já usam a versão nova. Depois, chame só deploy-tools: as atualizações " +
					"passam a ser automáticas.\n\n" + strings.Join(lines, "\n")
			},
			CLI: "sudo deploy-tools server install",
		}
		srv.Items = append(srv.Items, install)
	}
	return app
}

// listApps is env.P.Apps, with a missing apps root meaning no apps.
func listApps(env *Env) ([]provision.AppInfo, error) {
	apps, err := env.P.Apps()
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return apps, err
}

// selectApp offers the apps that pass filter, with their situation; empty
// is the error when none does.
func selectApp(env *Env, title string, filter func(provision.AppInfo) bool, empty string) (string, error) {
	apps, err := listApps(env)
	if err != nil {
		return "", err
	}
	nameW := 0
	for _, a := range apps {
		nameW = max(nameW, len([]rune(a.Name)))
	}
	var opts []huh.Option[string]
	for _, a := range apps {
		if filter == nil || filter(a) {
			opts = append(opts, huh.NewOption(appLine(env, a, nameW), a.Name))
		}
	}
	if len(opts) == 0 {
		return "", errors.New(empty)
	}
	var app string
	err = env.UI.Select(title, &app, opts...)
	return app, err
}

// needsMigration: still served straight from /var/www/<app>.
func needsMigration(a provision.AppInfo) bool { return !a.Managed }

// needsConf: migrated, but deploying without .deploy.conf (no hooks).
func needsConf(a provision.AppInfo) bool { return a.Managed && a.Conf == nil }

func iCreate(env *Env) error {
	var o provision.CreateOptions
	notExisting := func(s string) error {
		if err := validate.AppName(s); err != nil {
			return errors.New("use só letras, números, - e _")
		}
		if _, err := os.Lstat(env.Paths.AppDir(s)); err == nil {
			return fmt.Errorf("%s já existe", env.Paths.AppDir(s))
		}
		return nil
	}
	if err := env.UI.Run(
		huh.NewGroup(
			huh.NewInput().Title("Nome do app").
				Description("Vira a pasta em "+env.Paths.AppsRoot+" e o APP_NAME do workflow.").
				Placeholder("minha-api").Value(&o.App).Validate(notExisting),
			huh.NewSelect[string]().Title("Que tipo de projeto é?").Options(typeOptions()...).Value(&o.Type),
		),
	); err != nil {
		return err
	}
	ds, err := chooseDomains(env, o.App, true)
	if err != nil {
		return err
	}
	o.Domains = ds
	if err := askCertForApp(env, &o); err != nil {
		return err
	}
	var fields []huh.Field
	if validate.IsPHP(o.Type) {
		fields = append(fields, huh.NewConfirm().Title("Rodar as migrations do banco a cada deploy?").
			Description(release.MigrateCommand(o.Type)+", antes de publicar a release.").
			Affirmative("Sim").Negative("Não").Value(&o.MigrateOnDeploy))
	}
	fields = append(fields, huh.NewConfirm().Title("Colocar o site no ar agora?").
		Description("Ativa no Apache e recarrega. Escolha Não se o DNS ou o certificado ainda não estão prontos.").
		Affirmative("Sim").Negative("Não").Value(&o.Enable))
	if err := env.UI.Run(huh.NewGroup(fields...)); err != nil {
		return err
	}

	env.UI.Title("Resumo")
	https := "sim"
	if o.Cert.File == "" {
		if c, _ := env.P.ChooseCert(o.Domains, provision.CertChoice{}); c.Fallback {
			https = "não (sem certificado: só HTTP)"
		}
	}
	summary := [][2]string{
		{"app", o.App},
		{"tipo", typeName(o.Type)},
		{"domínios", strings.Join(o.Domains, ", ")},
		{"HTTPS", https},
		{"no ar agora", yesNo(o.Enable)},
	}
	if validate.IsPHP(o.Type) {
		summary = append(summary, [2]string{"migrations no deploy", yesNo(o.MigrateOnDeploy)})
	}
	env.UI.KV(summary...)
	env.UI.Println()
	if err := confirm(env, false, "Criar o app?", ""); err != nil {
		return err
	}
	return doCreate(env, o)
}

func iMigrate(env *Env) error {
	app, err := selectApp(env, "Qual app migrar?", needsMigration,
		"nenhum app precisa migrar: todos em "+env.Paths.AppsRoot+" já estão no modelo de releases")
	if err != nil {
		return err
	}
	o := provision.MigrateOptions{App: app}
	detected := detectLegacyType(env.Paths.AppDir(app))
	o.Type = detected
	if err := env.UI.Select("Que tipo de projeto é? (parece "+typeName(detected)+")", &o.Type, typeOptions()...); err != nil {
		return err
	}
	if validate.IsPHP(o.Type) {
		if o.MigrateOnDeploy, err = env.UI.Confirm("Rodar as migrations do banco a cada deploy?", release.MigrateCommand(o.Type)); err != nil {
			return err
		}
	}

	// Offer a manual pick when detection is ambiguous.
	root := env.Paths.AppDir(app)
	found, _ := apache.FindVhosts(env.Paths.SitesEnabled, root, filepath.Join(root, "public"))
	if len(found) != 1 {
		matches, _ := filepath.Glob(filepath.Join(env.Paths.SitesAvail, "*.conf"))
		if len(matches) == 0 {
			return errors.New("nenhum site do Apache encontrado em " + env.Paths.SitesAvail)
		}
		var opts []huh.Option[string]
		for _, m := range matches {
			opts = append(opts, huh.NewOption(filepath.Base(m), m))
		}
		title := "Não achei um site ativo do Apache apontando para " + root + ". Qual arquivo editar?"
		if len(found) > 1 {
			title = "Mais de um site do Apache aponta para " + root + ". Qual arquivo editar?"
		}
		if err := env.UI.Select(title, &o.Vhost, opts...); err != nil {
			return err
		}
	}
	return doMigrate(env, o, false)
}

func detectLegacyType(dir string) string {
	return appconf.DetectType(dir)
}

func iStatus(env *Env) error {
	app, err := selectApp(env, "Qual app?", nil, "nenhum app em "+env.Paths.AppsRoot)
	if err != nil {
		return err
	}
	return doStatus(env, app)
}

func iRollback(env *Env) error {
	app, err := selectApp(env, "Rollback de qual app?", func(a provision.AppInfo) bool { return a.Managed && len(a.Releases) > 1 },
		"nenhum app tem uma release anterior em disco para voltar")
	if err != nil {
		return err
	}
	rels, err := env.P.Releases().List(app)
	if err != nil {
		return err
	}
	now := env.now()
	var opts []huh.Option[string]
	for _, r := range rels {
		if !r.Current {
			opts = append(opts, huh.NewOption(releaseLabel(r.ID, now), r.ID))
		}
	}
	id, _ := previousRelease(rels)
	cur := env.P.Releases().CurrentID(app)
	if err := env.UI.Run(huh.NewGroup(huh.NewSelect[string]().Title("Voltar para qual release?").
		Description("No ar agora: " + releaseLabel(cur, now)).
		Options(opts...).Value(&id))); err != nil {
		return err
	}
	return doRollback(env, app, id, false)
}

func iConfig(env *Env) error {
	app, err := selectApp(env, "Configurar qual app?", needsConf,
		"todos os apps migrados já têm .deploy.conf (para mudar um: sudo deploy-tools app config <app>)")
	if err != nil {
		return err
	}
	return doConfig(env, app, "", nil)
}

func iEnable(env *Env) error {
	app, err := selectApp(env, "Ativar o site de qual app?", func(a provision.AppInfo) bool {
		return a.Vhost != "" && !a.VhostEnabled
	}, "nenhum site desativado")
	if err != nil {
		return err
	}
	return doEnable(env, app, false)
}

func iDisable(env *Env) error {
	app, err := selectApp(env, "Desativar o site de qual app?", func(a provision.AppInfo) bool {
		return a.Vhost != "" && a.VhostEnabled
	}, "nenhum site ativo")
	if err != nil {
		return err
	}
	return doDisable(env, app, false)
}

func iDomains(env *Env) error {
	app, err := selectApp(env, "Domínios de qual app?", func(a provision.AppInfo) bool { return a.Vhost != "" }, "nenhum app com site no Apache")
	if err != nil {
		return err
	}
	cur, err := env.P.Domains(app)
	if err != nil {
		return err
	}
	env.UI.KV([2]string{"domínios atuais", strings.Join(cur, ", ")})
	action := "add"
	if err := env.UI.Select("O que fazer?", &action,
		huh.NewOption("Adicionar domínios", "add"),
		huh.NewOption("Remover domínios", "remove"),
	); err != nil {
		return err
	}
	if action == "remove" {
		var rm []string
		opts := make([]huh.Option[string], 0, len(cur))
		for _, d := range cur {
			opts = append(opts, huh.NewOption(d, d))
		}
		if err := env.UI.Run(huh.NewGroup(huh.NewMultiSelect[string]().Title("Remover quais?").Options(opts...).Value(&rm))); err != nil {
			return err
		}
		if len(rm) == 0 {
			return ui.ErrAborted
		}
		return env.P.RemoveDomains(app, rm)
	}
	add, err := chooseDomains(env, "", false)
	if err != nil {
		return err
	}
	return env.P.AddDomains(app, add)
}

func iCert(env *Env) error {
	app, err := selectApp(env, "Certificado de qual app?", func(a provision.AppInfo) bool { return a.Vhost != "" }, "nenhum app com site no Apache")
	if err != nil {
		return err
	}
	c, err := env.P.UpdateCert(app, provision.CertChoice{})
	if err != nil || !c.Fallback {
		return err
	}
	given, err := askCert(env, "Nenhum certificado no servidor cobre os domínios. Informar um agora?",
		"Arquivos PEM já no servidor (ex.: fullchain.pem e privkey.pem).")
	if err != nil || given.File == "" {
		return err
	}
	_, err = env.P.UpdateCert(app, given)
	return err
}

func iAddKey(env *Env) error {
	choice, path, err := askKeySource(env, false)
	if err != nil {
		return err
	}
	if choice == "generate" {
		return doGenerateKey(env, keyGen{})
	}
	if err := env.P.AddKey(path); err != nil {
		return err
	}
	if err := ensureDeployHost(env, false); err != nil {
		return err
	}
	env.UI.Println()
	s := env.P.Secrets("")
	printSecrets(env, s, privateKeyName(path), secretsTarget{})
	return offerCopy(env, ghCommands(s, privateKeyName(path), secretsTarget{}), ghScript(s, privateKeyName(path)))
}

func iInstall(env *Env) error {
	if err := env.P.Install(env.Self); err != nil {
		return err
	}
	env.install = nil
	env.UI.OK("Instalado. Os próximos deploys da CI já usam a versão nova.")
	return nil
}

func iSetup(env *Env) error {
	choice, path, err := askKeySource(env, true)
	if err != nil {
		return err
	}
	if err := env.P.Setup(env.Self, path); err != nil {
		return err
	}
	env.install = nil
	if err := ensureDeployHost(env, true); err != nil {
		return err
	}
	env.UI.OK("Servidor pronto.")
	switch choice {
	case "generate":
		return doGenerateKey(env, keyGen{})
	case "file":
		env.UI.Println()
		s := env.P.Secrets("")
		printSecrets(env, s, privateKeyName(path), secretsTarget{})
		return offerCopy(env, ghCommands(s, privateKeyName(path), secretsTarget{}), ghScript(s, privateKeyName(path)))
	}
	return nil
}

func fileExists(s string) error {
	if _, err := os.Stat(s); err != nil {
		return errors.New("arquivo não encontrado")
	}
	return nil
}

func yesNo(b bool) string { return map[bool]string{true: "sim", false: "não"}[b] }

// nextStep is the TUI's notice line: what to do next on this server, in
// the order a new server needs it. It runs on every redraw: no network,
// no subprocess.
func nextStep(env *Env) string {
	if env.RequireRoot() != nil {
		return ""
	}
	if !env.P.DeployUserExists() || !fileExistsAt(env.Paths.BinaryPath()) {
		return "Servidor ainda não preparado: comece por \"Preparar servidor\"."
	}
	if s := env.P.Secrets(""); s.CIKeys != nil && len(s.CIKeys) == 0 {
		return "Nenhuma chave do GitHub ainda: use \"Gerar ou adicionar chave\" para os repositórios poderem publicar."
	}
	apps, _ := listApps(env)
	if len(apps) == 0 {
		return "Nenhum app ainda: crie o primeiro em \"Criar app\"."
	}
	for _, a := range apps {
		switch {
		case needsMigration(a):
			return a.Name + " serve sem versões (sem deploy pela CI): veja \"Converter site existente\"."
		case a.Vhost != "" && !a.VhostEnabled:
			return "O site de " + a.Name + " está fora do ar: quando DNS e certificado estiverem prontos, use \"Colocar no ar\"."
		case a.Vhost != "" && a.CertFile == "":
			return a.Name + " está só em HTTP: instale um certificado e use \"HTTPS do site\"."
		}
	}
	return ""
}

func fileExistsAt(p string) bool { _, err := os.Stat(p); return err == nil }
