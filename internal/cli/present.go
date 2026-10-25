package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/vinicgobbi/deploy-tools/internal/config"
	"github.com/vinicgobbi/deploy-tools/internal/provision"
	"github.com/vinicgobbi/deploy-tools/internal/release"
	"github.com/vinicgobbi/deploy-tools/internal/ui"
	"github.com/vinicgobbi/deploy-tools/internal/validate"
)

// How apps and releases are shown to people: plain words instead of the
// on-disk layout (vhost, current, .deploy.conf).

type appState struct {
	Icon, Text string
	Tone       ui.Tone
}

func stateOf(a provision.AppInfo) appState {
	switch {
	case !a.Managed:
		return appState{"⚠", "precisa migrar", ui.Bad}
	case a.Vhost == "":
		return appState{"⚠", "sem site no Apache", ui.Bad}
	case !a.VhostEnabled:
		return appState{"○", "site desativado", ui.Idle}
	case a.Current == "":
		return appState{"○", "aguardando o 1º deploy", ui.Idle}
	}
	return appState{"●", "no ar", ui.Good}
}

func (s appState) render(u *ui.UI) string { return u.Tone(s.Tone, s.Icon+" "+s.Text) }

func typeName(t string) string {
	switch t {
	case validate.Laravel:
		return "Laravel"
	case validate.Symfony:
		return "Symfony"
	case validate.React:
		return "React"
	case validate.Angular:
		return "Angular"
	case validate.Static:
		return "Estático"
	}
	return t
}

func appType(a provision.AppInfo) string {
	if a.Conf == nil {
		return "—"
	}
	return typeName(a.Conf.Type)
}

func (env *Env) now() time.Time {
	if env.P.Now != nil {
		return env.P.Now()
	}
	return time.Now()
}

// releaseTime parses the timestamp prefix of a release id
// (20260911153000-a99d021), in the server's local time.
func releaseTime(id string) (time.Time, string, bool) {
	stamp, rest, _ := strings.Cut(id, "-")
	t, err := time.ParseInLocation("20060102150405", stamp, time.Local)
	return t, rest, err == nil
}

// releaseLabel turns 20260911153000-a99d021 into
// "11/09 15:30 · a99d021 · há 2 dias".
func releaseLabel(id string, now time.Time) string {
	t, rest, ok := releaseTime(id)
	if !ok {
		return id
	}
	layout := "02/01 15:04"
	if t.Year() != now.Year() {
		layout = "02/01/2006 15:04"
	}
	if rest == "pre-migracao" {
		rest = "conteúdo de antes da migração"
	}
	parts := []string{t.Format(layout)}
	if rest != "" {
		parts = append(parts, rest)
	}
	return strings.Join(append(parts, ago(now.Sub(t))), " · ")
}

// releaseAgo is the "há 2 dias" part alone ("" if the id has no timestamp).
func releaseAgo(id string, now time.Time) string {
	t, _, ok := releaseTime(id)
	if !ok {
		return ""
	}
	return ago(now.Sub(t))
}

func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "agora há pouco"
	case d < time.Hour:
		return fmt.Sprintf("há %d min", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("há %d h", int(d.Hours()))
	case d < 48*time.Hour:
		return "ontem"
	}
	return fmt.Sprintf("há %d dias", int(d.Hours()/24))
}

// appLine is one app in the home menu: name, type, situation, last deploy.
func appLine(env *Env, a provision.AppInfo, nameW int) string {
	st := stateOf(a).render(env.UI)
	if a.Managed && a.Current != "" && a.VhostEnabled {
		if when := releaseAgo(a.Current, env.now()); when != "" {
			st += env.UI.Faint(" · deploy " + when)
		}
	}
	return fmt.Sprintf("%-*s  %-8s  %s", nameW, a.Name, appType(a), st)
}

// printAppCard is the status screen of an app: what it is, whether it is
// up, and what (if anything) is missing.
func printAppCard(env *Env, info provision.AppInfo) {
	now := env.now()
	env.UI.Title(info.Name)
	pairs := [][2]string{{"situação", stateOf(info).render(env.UI)}}
	for i, d := range info.Domains {
		label := ""
		if i == 0 {
			label = "endereço"
		}
		scheme := "https://"
		if info.CertFile == "" {
			scheme = "http://"
		}
		pairs = append(pairs, [2]string{label, scheme + d})
	}
	if info.Vhost != "" {
		cert := info.CertFile
		switch cert {
		case "":
			cert = "nenhum — site só em HTTP  " + env.UI.Faint("(com o certificado instalado: sudo deploy-tools app cert "+info.Name+")")
		case config.FallbackCert:
			cert += "  " + env.UI.Faint("(autoassinado provisório — sudo deploy-tools app cert "+info.Name+")")
		}
		pairs = append(pairs, [2]string{"certificado", cert})
	}
	deploy := "sem .deploy.conf — o deploy só troca os arquivos (sem hooks)"
	if info.Conf != nil {
		deploy = typeName(info.Conf.Type)
		if validate.IsPHP(info.Conf.Type) {
			deploy += " · migrations a cada deploy: " + yesNo(info.Conf.Migrate)
		}
		if info.Conf.Type == validate.Laravel {
			deploy += " · agendador: " + yesNo(info.Conf.Schedule)
		}
	}
	pairs = append(pairs, [2]string{"tipo", deploy})
	if info.Current != "" {
		pairs = append(pairs, [2]string{"release ativa", releaseLabel(info.Current, now)})
	}
	pairs = append(pairs, [2]string{"pasta", env.Paths.AppDir(info.Name)})
	if info.Vhost != "" {
		pairs = append(pairs, [2]string{"site Apache", info.Vhost + "  " +
			env.UI.Faint(map[bool]string{true: "(ativo)", false: "(desativado)"}[info.VhostEnabled])})
	}
	env.UI.KV(pairs...)

	if info.Managed && len(info.Releases) > 0 {
		env.UI.Println()
		env.UI.Println(env.UI.Faint(fmt.Sprintf("Releases em disco (o deploy mantém as %d mais recentes)", env.Paths.KeepReleases)))
		printReleases(env, info.Releases, false)
	}

	var todo []string
	switch {
	case !info.Managed:
		todo = append(todo, "Este app serve direto de "+env.Paths.AppDir(info.Name)+", sem releases: não recebe deploy da CI nem tem rollback.",
			"Converta sem tirar do ar: sudo deploy-tools app migrate "+info.Name)
	case info.Vhost == "":
		todo = append(todo, "Nenhum site do Apache aponta para "+env.Paths.AppDir(info.Name)+".")
	case !info.VhostEnabled:
		todo = append(todo, "O site existe mas está desativado. Com DNS e certificado prontos:",
			"sudo deploy-tools app enable "+info.Name)
	case info.Current == "":
		todo = append(todo, "Ainda sem deploy: faça push na branch de deploy do repositório.")
	}
	if info.Managed && info.Conf == nil {
		todo = append(todo, "Sem .deploy.conf: grave o tipo com sudo deploy-tools app config "+info.Name)
	}
	if c := info.Conf; c != nil {
		wantCron := c.Schedule && c.Type == validate.Laravel
		switch {
		case wantCron && !info.Scheduled:
			todo = append(todo, "O agendador está ligado, mas falta "+env.Paths.CronFile(info.Name)+
				". Recrie com: sudo deploy-tools app config "+info.Name+" --schedule")
		case !wantCron && info.Scheduled:
			todo = append(todo, env.Paths.CronFile(info.Name)+" existe, mas o agendador está desligado no .deploy.conf. "+
				"Remova com: sudo deploy-tools app config "+info.Name+" --no-schedule")
		}
	}
	if len(todo) > 0 {
		env.UI.Println()
		for _, t := range todo {
			env.UI.Hint("%s", t)
		}
	}
}

// printReleases lists newest first; withIDs adds the full id (what
// `release rollback` takes) to each line.
func printReleases(env *Env, rels []release.Release, withIDs bool) {
	if len(rels) == 0 {
		env.UI.Println("  (nenhuma release ainda — o primeiro deploy cria)")
		return
	}
	now := env.now()
	for _, r := range rels {
		id := ""
		if withIDs {
			id = "  " + env.UI.Faint(r.ID)
		}
		if r.Current {
			env.UI.Println(env.UI.Tone(ui.Good, "  ● "+releaseLabel(r.ID, now)+"  (ativa)") + id)
		} else {
			env.UI.Println("    " + releaseLabel(r.ID, now) + id)
		}
	}
}
