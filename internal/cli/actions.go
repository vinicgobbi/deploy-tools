package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/vinicgobbi/deploy-tools/internal/appconf"
	"github.com/vinicgobbi/deploy-tools/internal/config"
	"github.com/vinicgobbi/deploy-tools/internal/provision"
	"github.com/vinicgobbi/deploy-tools/internal/release"
	"github.com/vinicgobbi/deploy-tools/internal/ui"
	"github.com/vinicgobbi/deploy-tools/internal/validate"
)

// Actions shared by the flag-driven commands and the interactive menu.

func typeOptions() []huh.Option[string] {
	return []huh.Option[string]{
		huh.NewOption("Laravel   PHP; .env e storage/ são mantidos entre deploys", validate.Laravel),
		huh.NewOption("React     SPA buildada com Vite (dist/)", validate.React),
		huh.NewOption("Estático  site já pronto, ex.: mkdocs ou HTML", validate.Static),
	}
}

// confirm asks unless yes is set; without a TTY, --yes is required.
func confirm(env *Env, yes bool, title, description string) error {
	if yes {
		return nil
	}
	if !env.UI.Interactive {
		return errors.New("confirmação necessária: rode num terminal ou passe --yes")
	}
	ok, err := env.UI.Confirm(title, description)
	if err != nil {
		return err
	}
	if !ok {
		return ui.ErrAborted
	}
	return nil
}

func doCreate(env *Env, o provision.CreateOptions) error {
	if err := env.RequireRoot(); err != nil {
		return err
	}
	o.Defaults(env.Paths.BaseDomains)
	if err := env.P.Create(o); err != nil {
		return err
	}
	appDir := env.Paths.AppDir(o.App)
	env.UI.OK("App %s criado.", o.App)
	env.UI.KV(
		[2]string{"pasta", appDir + "  " + env.UI.Faint("(a 1ª release chega no 1º deploy)")},
		[2]string{"site Apache", env.Paths.VhostFile(o.App)},
		[2]string{"endereço", "https://" + strings.Join(o.Domains, "  https://")},
	)
	var steps []string
	if !o.Enable {
		steps = append(steps, "Revise "+env.Paths.VhostFile(o.App)+" (domínio, certificado).")
	}
	steps = append(steps, "Confirme que o DNS de "+strings.Join(o.Domains, ", ")+" aponta para cá.")
	if info, _ := env.P.App(o.App); info.CertFile == config.FallbackCert {
		steps = append(steps, "Emita um certificado para os domínios (ex.: certbot certonly --webroot) e rode `sudo deploy-tools app cert "+o.App+"`.")
	}
	if o.Type == validate.Laravel {
		steps = append(steps, "Preencha "+filepath.Join(env.Paths.SharedDir(o.App), ".env")+".")
	}
	if !o.Enable {
		steps = append(steps, "Coloque no ar: sudo deploy-tools app enable "+o.App)
	}
	steps = append(steps, "No repositório, use APP_NAME="+o.App+" no workflow e faça push em main.")
	env.UI.Steps("Próximos passos", steps...)
	return nil
}

func doMigrate(env *Env, o provision.MigrateOptions, yes bool) error {
	if err := env.RequireRoot(); err != nil {
		return err
	}
	pl, err := env.P.PlanMigrate(o)
	if err != nil {
		return err
	}
	env.UI.Title("Migração de " + o.App + " (" + typeName(o.Type) + ")")
	env.UI.KV(
		[2]string{"pasta", pl.Root},
		[2]string{"site Apache", pl.Vhost},
		[2]string{"DocumentRoot", pl.OldDocRoot + "  →  " + pl.NewDocRoot},
		[2]string{"release", "releases/" + pl.ReleaseID + "  (o conteúdo atual)"},
	)
	if o.Type == validate.Laravel {
		env.UI.KV([2]string{"shared/", ".env e storage/ saem da release e viram symlinks"})
	}
	env.UI.Hint("O Apache é recarregado depois de validar a config; se ela tiver erro, nada muda.")
	env.UI.Println()
	if err := confirm(env, yes, "Migrar "+o.App+" agora?", "O site continua no ar durante a migração."); err != nil {
		return err
	}
	if err := env.P.Migrate(pl); err != nil {
		return err
	}
	env.UI.OK("%s migrado. current -> releases/%s", o.App, pl.ReleaseID)
	for _, w := range pl.Warnings() {
		env.UI.Warn("%s", w)
	}
	env.UI.Println("O próximo push em main já cria a release seguinte normalmente.")
	return nil
}

// siteFile is the Apache site of app, for confirmations ("" if none).
func siteFile(env *Env, app string) string {
	if info, err := env.P.App(app); err == nil {
		return info.Vhost
	}
	return ""
}

func doEnable(env *Env, app string, yes bool) error {
	if err := env.RequireRoot(); err != nil {
		return err
	}
	if err := confirm(env, yes, "Colocar "+app+" no ar?",
		"a2ensite "+siteFile(env, app)+". A config do Apache é testada antes e depois; "+
			"só recarrega se passar, senão desfaz."); err != nil {
		return err
	}
	return env.P.Enable(app)
}

func doDisable(env *Env, app string, yes bool) error {
	if err := env.RequireRoot(); err != nil {
		return err
	}
	if err := confirm(env, yes, "Tirar "+app+" do ar?",
		"a2dissite "+siteFile(env, app)+". O site para de responder; os arquivos, as releases e o "+
			"deploy continuam como estão. A config é testada antes e depois do reload."); err != nil {
		return err
	}
	return env.P.Disable(app)
}

// previousRelease is the newest release older than current.
func previousRelease(rels []release.Release) (string, error) {
	for i, r := range rels {
		if r.Current {
			if i+1 < len(rels) {
				return rels[i+1].ID, nil
			}
			return "", errors.New("não há release anterior à atual")
		}
	}
	return "", errors.New("current não aponta para nenhuma release")
}

func doRollback(env *Env, app, id string, yes bool) error {
	if err := env.RequireRoot(); err != nil {
		return err
	}
	m := env.P.Releases()
	m.Out = io.Discard // the CI's "rolled back" line; the CLI prints its own
	rels, err := m.List(app)
	if err != nil {
		return hint(err, app)
	}
	if id == "" {
		if id, err = previousRelease(rels); err != nil {
			return err
		}
	}
	cur := m.CurrentID(app)
	if id == cur {
		return fmt.Errorf("%s já é a release atual de %s", id, app)
	}
	now := env.now()
	if err := confirm(env, yes, fmt.Sprintf("Voltar %s para a release de %s?", app, releaseLabel(id, now)),
		"No ar agora: "+releaseLabel(cur, now)+". O próximo push em main publica uma release nova normalmente."); err != nil {
		return err
	}
	if err := m.Rollback(app, id); err != nil {
		return hint(err, app)
	}
	env.UI.OK("%s voltou para %s.", app, releaseLabel(id, now))
	return nil
}

func doStatus(env *Env, app string) error {
	if err := validate.AppName(app); err != nil {
		return err
	}
	if _, err := os.Stat(env.Paths.AppDir(app)); err != nil {
		return hint(fmt.Errorf("%w: %s", release.ErrUnknownApp, app), app)
	}
	info, err := env.P.App(app)
	if err != nil {
		return err
	}
	printAppCard(env, info)
	return nil
}

func doList(env *Env) error {
	apps, err := listApps(env)
	if err != nil {
		return err
	}
	if len(apps) == 0 {
		env.UI.Println("Nenhum app em " + env.Paths.AppsRoot + ". Crie com: sudo deploy-tools app create <app>")
		return nil
	}
	now := env.now()
	rows := [][]string{{"APP", "TIPO", "SITUAÇÃO", "ÚLTIMO DEPLOY", "RELEASES"}}
	for _, a := range apps {
		st := stateOf(a)
		last, n := "-", "-"
		if a.Managed {
			n = fmt.Sprint(len(a.Releases))
			if a.Current != "" {
				last = orDash(releaseAgo(a.Current, now))
			}
		}
		rows = append(rows, []string{a.Name, appType(a), st.Icon + " " + st.Text, last, n})
	}
	printTable(env, rows)
	return nil
}

func doKeys(env *Env) error {
	keys, err := env.P.Keys()
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		env.UI.Println("Nenhuma chave em " + env.Paths.AuthorizedKeys() + ".")
		env.UI.Hint("Autorize a da CI com: sudo deploy-tools key add ci_deploy_key.pub")
		return nil
	}
	rows := [][]string{{"FINGERPRINT", "TIPO", "COMENTÁRIO", "RESTRIÇÃO"}}
	for _, k := range keys {
		restr := "SEM command= (acesso de shell!)"
		if k.Command != "" {
			restr = k.Command
		}
		rows = append(rows, []string{k.Fingerprint(), k.Type, orDash(k.Comment), restr})
	}
	printTable(env, rows)
	return nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func printTable(env *Env, rows [][]string) {
	widths := make([]int, len(rows[0]))
	for _, r := range rows {
		for i, c := range r {
			widths[i] = max(widths[i], len([]rune(c)))
		}
	}
	for ri, r := range rows {
		var b strings.Builder
		for i, c := range r {
			b.WriteString(c + strings.Repeat(" ", widths[i]-len([]rune(c))+2))
		}
		line := strings.TrimRight(b.String(), " ")
		if ri == 0 {
			line = env.UI.Faint(line)
		}
		env.UI.Println(line)
	}
}

func doConfig(env *Env, app, typ string, migrate *bool) error {
	if err := env.RequireRoot(); err != nil {
		return err
	}
	if err := env.P.Releases().ValidateApp(app); err != nil {
		return hint(err, app)
	}
	existing, ok, err := appconf.Read(env.Paths.AppDir(app))
	if err != nil {
		return err
	}
	c := existing
	if typ != "" {
		c.Type = typ
	} else if !ok || c.Type == "" {
		detected := appconf.DetectType(env.Paths.CurrentLink(app))
		c.Type = detected
		if env.UI.Interactive {
			if err := env.UI.Select("Tipo de "+app+" (detectado: "+detected+")", &c.Type, typeOptions()...); err != nil {
				return err
			}
		} else {
			env.UI.Println("Tipo detectado: " + detected)
		}
	}
	if err := validate.Type(c.Type); err != nil {
		return err
	}
	if migrate != nil {
		c.LaravelMigrate = *migrate
	} else if c.Type == validate.Laravel && env.UI.Interactive {
		if c.LaravelMigrate, err = env.UI.Confirm("Rodar php artisan migrate --force a cada deploy?", ""); err != nil {
			return err
		}
	}
	if err := env.P.Configure(app, c); err != nil {
		return err
	}
	if c.Type == validate.Laravel {
		env.UI.Println("Próximos deploys rodam storage:link, config:cache, route:cache, view:cache" +
			map[bool]string{true: " e migrate --force.", false: "."}[c.LaravelMigrate])
	}
	return nil
}

// hint adds a next step to common errors.
func hint(err error, app string) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, release.ErrUnknownApp):
		return fmt.Errorf("app '%s' não existe — crie com `sudo deploy-tools app create %s` (ou veja `deploy-tools app list`)", app, app)
	case errors.Is(err, release.ErrReleaseNotFound):
		return fmt.Errorf("%w — veja as disponíveis com `deploy-tools release list %s`", err, app)
	}
	return err
}
