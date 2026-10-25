package provision

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/vinicgobbi/deploy-tools/internal/appconf"
	"github.com/vinicgobbi/deploy-tools/internal/validate"
)

// ScheduleEntry is the /etc/cron.d content that runs the Laravel scheduler
// of app every minute (sub-minute tasks keep schedule:run alive until the
// minute ends). It goes through current/, so every deploy is picked up
// without touching the file, and does nothing before the first release.
// It runs as the web user: the app already runs as it under mod_php, and
// the cache and log files the scheduler creates stay writable by the site.
func (p *Provisioner) ScheduleEntry(app string) string {
	current := p.Paths.CurrentLink(app)
	return fmt.Sprintf(`# Gerado por deploy-tools: agendador do Laravel de %[1]s.
# Para desligar: sudo deploy-tools app config %[1]s --no-schedule
PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
* * * * * %[2]s [ -f %[3]s/artisan ] && cd %[3]s && php artisan schedule:run >> /dev/null 2>&1
`, app, p.Paths.WebUser, current)
}

// ScheduleInstalled reports whether the app's cron entry exists.
func (p *Provisioner) ScheduleInstalled(app string) bool {
	return exists(p.Paths.CronFile(app))
}

// syncSchedule makes the cron entry match the conf: written (root, 0644)
// when the Laravel scheduler is on, removed otherwise. The conf is the
// source of truth; the CI key can change neither.
func (p *Provisioner) syncSchedule(app string, c appconf.Conf) error {
	file := p.Paths.CronFile(app)
	if !c.Schedule || c.Type != validate.Laravel {
		err := os.Remove(file)
		if err == nil {
			p.say("Agendador desligado: removido %s", file)
			return nil
		}
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	want := p.ScheduleEntry(app)
	if b, err := os.ReadFile(file); err == nil && string(b) == want {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(file), ".deploy-tools-"+app+".tmp") // with a dot: cron skips it
	if err := os.WriteFile(tmp, []byte(want), 0o644); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, file); err != nil {
		return err
	}
	p.say("Agendador ligado: %s roda php artisan schedule:run a cada minuto (%s)", p.Paths.WebUser, file)
	return nil
}
