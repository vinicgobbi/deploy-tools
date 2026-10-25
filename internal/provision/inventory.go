package provision

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vinicgobbi/deploy-tools/internal/apache"
	"github.com/vinicgobbi/deploy-tools/internal/appconf"
	"github.com/vinicgobbi/deploy-tools/internal/release"
	"github.com/vinicgobbi/deploy-tools/internal/validate"
)

type AppInfo struct {
	Name         string
	Managed      bool          // has releases/ (deployable by the CI)
	Conf         *appconf.Conf // nil: no .deploy.conf (legacy behavior)
	Current      string
	Releases     []release.Release
	Vhost        string
	VhostEnabled bool
	DocRoot      string
	Domain       string   // ServerName of the vhost
	Domains      []string // ServerName + ServerAlias
	CertFile     string   // SSLCertificateFile of the vhost
	Scheduled    bool     // the Laravel scheduler cron entry exists
}

// Apps lists every directory under AppsRoot, managed or not.
func (p *Provisioner) Apps() ([]AppInfo, error) {
	entries, err := os.ReadDir(p.Paths.AppsRoot)
	if err != nil {
		return nil, err
	}
	var out []AppInfo
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || validate.AppName(e.Name()) != nil {
			continue
		}
		info, err := p.App(e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (p *Provisioner) App(name string) (AppInfo, error) {
	info := AppInfo{Name: name}
	dir := p.Paths.AppDir(name)
	if fi, err := os.Stat(p.Paths.ReleasesDir(name)); err == nil && fi.IsDir() {
		info.Managed = true
		m := p.Releases()
		rels, err := m.List(name)
		if err != nil {
			return info, err
		}
		info.Releases, info.Current = rels, m.CurrentID(name)
	}
	if c, ok, err := appconf.Read(dir); err != nil {
		return info, err
	} else if ok {
		info.Conf = &c
	}
	info.Scheduled = p.ScheduleInstalled(name)

	if exists(p.Paths.VhostFile(name)) {
		info.Vhost = p.Paths.VhostFile(name)
	} else if found, _ := apache.FindVhosts(p.Paths.SitesEnabled,
		dir, filepath.Join(dir, "public"),
		p.Paths.CurrentLink(name), filepath.Join(p.Paths.CurrentLink(name), "public"),
	); len(found) > 0 {
		info.Vhost = found[0]
	}
	if info.Vhost != "" {
		if b, err := os.ReadFile(info.Vhost); err == nil {
			if roots := apache.DocumentRoots(string(b)); len(roots) > 0 {
				info.DocRoot = roots[len(roots)-1]
			}
			info.Domain = apache.ServerName(string(b))
			info.Domains = apache.Domains(string(b))
			info.CertFile, _, _ = apache.CertFiles(string(b))
		}
		info.VhostEnabled = p.vhostEnabled(info.Vhost)
	}
	return info, nil
}

func (p *Provisioner) vhostEnabled(vhost string) bool {
	entries, err := os.ReadDir(p.Paths.SitesEnabled)
	if err != nil {
		return false
	}
	want, _ := filepath.EvalSymlinks(vhost)
	for _, e := range entries {
		if got, err := filepath.EvalSymlinks(filepath.Join(p.Paths.SitesEnabled, e.Name())); err == nil && got == want {
			return true
		}
	}
	return false
}

// Configure writes .deploy.conf for an existing managed app and keeps the
// Laravel scheduler cron entry in line with it.
func (p *Provisioner) Configure(app string, c appconf.Conf) error {
	if err := validate.AppName(app); err != nil {
		return err
	}
	if err := p.Releases().ValidateApp(app); err != nil {
		return err
	}
	if !validate.IsPHP(c.Type) {
		c.Migrate = false
	}
	if c.Type != validate.Laravel {
		c.Schedule = false
	}
	if err := appconf.Write(p.Paths.AppDir(app), c); err != nil {
		return err
	}
	p.say("Gravado %s", appconf.Path(p.Paths.AppDir(app)))
	return p.syncSchedule(app, c)
}
