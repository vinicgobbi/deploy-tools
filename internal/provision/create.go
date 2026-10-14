package provision

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vinicgobbi/deploy-tools/internal/apache"
	"github.com/vinicgobbi/deploy-tools/internal/appconf"
	"github.com/vinicgobbi/deploy-tools/internal/run"
	"github.com/vinicgobbi/deploy-tools/internal/validate"
)

type CreateOptions struct {
	App             string
	Type            string
	Domains         []string   // first is the ServerName; default: <app><DomainSuffix>, when the suffix is set
	Cert            CertChoice // optional explicit certificate (File+Key[+Chain]); found automatically otherwise
	MigrateOnDeploy bool       // laravel only
	Enable          bool       // a2ensite + configtest + reload
}

func (o *CreateOptions) Defaults(suffix string) {
	o.Domains = NormalizeDomains(o.Domains)
	if len(o.Domains) == 0 && o.App != "" && suffix != "" {
		o.Domains = []string{o.App + suffix}
	}
}

// CheckCreate validates everything up front so a failed create never
// leaves half a structure behind.
func (p *Provisioner) CheckCreate(o CreateOptions) error {
	if err := validate.AppName(o.App); err != nil {
		return err
	}
	if err := validate.Type(o.Type); err != nil {
		return err
	}
	if err := validateDomains(o.Domains); err != nil {
		return err
	}
	if exists(p.Paths.AppDir(o.App)) {
		return fmt.Errorf("já existe: %s (se é um app em produção fora do padrão, use `deploy-tools app migrate %s`)", p.Paths.AppDir(o.App), o.App)
	}
	if exists(p.Paths.VhostFile(o.App)) {
		return fmt.Errorf("vhost já existe: %s", p.Paths.VhostFile(o.App))
	}
	return nil
}

func (p *Provisioner) Create(o CreateOptions) error {
	o.Defaults(p.Paths.DomainSuffix)
	if err := p.CheckCreate(o); err != nil {
		return err
	}
	// With --enable, a broken Apache config would only show up after the
	// app was created; refuse up front instead.
	if o.Enable {
		if err := apache.ConfigTest(p.Runner); err != nil {
			return fmt.Errorf("%w: %w", apache.ErrConfigAlreadyBroken, err)
		}
	}
	appDir := p.Paths.AppDir(o.App)
	p.say("Criando %s (tipo: %s)...", appDir, o.Type)
	if err := p.mkdirShared(appDir, p.Paths.ReleasesDir(o.App), p.Paths.SharedDir(o.App)); err != nil {
		return err
	}

	docRoot, dirRoot := p.Paths.CurrentLink(o.App), appDir
	if o.Type == validate.Laravel {
		// Standard Laravel writable/persistent paths, kept outside releases/
		// and symlinked into every new release.
		sh := p.Paths.SharedDir(o.App)
		if err := p.mkdirShared(
			filepath.Join(sh, "storage"),
			filepath.Join(sh, "storage/app/public"),
			filepath.Join(sh, "storage/framework/cache"),
			filepath.Join(sh, "storage/framework/sessions"),
			filepath.Join(sh, "storage/framework/views"),
			filepath.Join(sh, "storage/logs"),
		); err != nil {
			return err
		}
		if err := p.emptyEnv(filepath.Join(sh, ".env")); err != nil {
			return err
		}
		p.say("Criado %s vazio — preencha antes do primeiro deploy.", filepath.Join(sh, ".env"))
		docRoot = filepath.Join(docRoot, "public")
		dirRoot = docRoot
	}

	if err := appconf.Write(appDir, appconf.Conf{Type: o.Type, LaravelMigrate: o.MigrateOnDeploy && o.Type == validate.Laravel}); err != nil {
		return err
	}

	if enabled, err := apache.EnsureRewrite(p.Runner); err != nil {
		return fmt.Errorf("ativando mod_rewrite: %w", err)
	} else if enabled {
		p.say("mod_rewrite ativado.")
	}

	cert, err := p.ChooseCert(o.Domains, o.Cert)
	if err != nil {
		return err
	}
	vhost, err := apache.Render(apache.VhostData{
		App: o.App, Type: o.Type, Domains: o.Domains, DocRoot: docRoot, DirRoot: dirRoot,
		CertFile: cert.File, CertKey: cert.Key, CertChain: cert.Chain,
	})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(p.Paths.SitesAvail, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(p.Paths.VhostFile(o.App), []byte(vhost), 0o644); err != nil {
		return err
	}
	p.say("Site do Apache escrito em %s", p.Paths.VhostFile(o.App))
	p.sayCert(cert)

	if o.Enable {
		return p.Enable(o.App)
	}
	return nil
}

func (p *Provisioner) emptyEnv(path string) error {
	if exists(path) {
		return nil
	}
	if err := os.WriteFile(path, nil, 0o640); err != nil {
		return err
	}
	if err := os.Chmod(path, 0o640); err != nil {
		return err
	}
	return p.Owner.Chown(path, p.Paths.DeployUser, p.Paths.WebGroup, false)
}

// site resolves the Apache site of app: its file in sites-available and
// the name a2ensite/a2dissite take (the file name without .conf).
func (p *Provisioner) site(app string) (name, file string, enabled bool, err error) {
	if err := validate.AppName(app); err != nil {
		return "", "", false, err
	}
	info, err := p.App(app)
	if err != nil {
		return "", "", false, err
	}
	if info.Vhost == "" {
		return "", "", false, fmt.Errorf("%s não tem site no Apache (nada em %s/%s.conf nem ativo apontando para %s)",
			app, p.Paths.SitesAvail, app, p.Paths.AppDir(app))
	}
	dir, _ := filepath.EvalSymlinks(filepath.Dir(info.Vhost))
	avail, _ := filepath.EvalSymlinks(p.Paths.SitesAvail)
	base := filepath.Base(info.Vhost)
	if dir != avail || !strings.HasSuffix(base, ".conf") {
		return "", "", false, fmt.Errorf("o site de %s (%s) não é um .conf de %s; a2ensite/a2dissite só tratam esses",
			app, info.Vhost, p.Paths.SitesAvail)
	}
	return strings.TrimSuffix(base, ".conf"), info.Vhost, info.VhostEnabled, nil
}

// Enable puts app's site on the air with a2ensite. The Apache config is
// tested before (it must already be valid) and after the change, and only
// then reloaded; on failure the site is disabled again.
func (p *Provisioner) Enable(app string) error {
	name, file, enabled, err := p.site(app)
	if err != nil {
		return err
	}
	if enabled {
		p.say("Site %s já está ativo.", name)
		return nil
	}
	err = apache.Change(p.Runner,
		func() error {
			_, err := run.Output(p.Runner, run.Cmd{Name: "a2ensite", Args: []string{name}})
			return err
		},
		func() { _, _ = run.Output(p.Runner, run.Cmd{Name: "a2dissite", Args: []string{name}}) })
	if err != nil {
		return err
	}
	p.say("Site %s (%s) ativado e Apache recarregado.", name, file)
	return nil
}

// Disable takes app's site off the air with a2dissite, with the same
// checks as Enable; on failure the site is enabled again.
func (p *Provisioner) Disable(app string) error {
	name, file, enabled, err := p.site(app)
	if err != nil {
		return err
	}
	if !enabled {
		p.say("Site %s já está desativado.", name)
		return nil
	}
	err = apache.Change(p.Runner,
		func() error {
			_, err := run.Output(p.Runner, run.Cmd{Name: "a2dissite", Args: []string{name}})
			return err
		},
		func() { _, _ = run.Output(p.Runner, run.Cmd{Name: "a2ensite", Args: []string{name}}) })
	if err != nil {
		return err
	}
	p.say("Site %s (%s) desativado e Apache recarregado.", name, file)
	return nil
}
