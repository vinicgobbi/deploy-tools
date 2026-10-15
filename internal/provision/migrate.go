package provision

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/vinicgobbi/deploy-tools/internal/apache"
	"github.com/vinicgobbi/deploy-tools/internal/appconf"
	"github.com/vinicgobbi/deploy-tools/internal/release"
	"github.com/vinicgobbi/deploy-tools/internal/validate"
)

type MigrateOptions struct {
	App             string
	Type            string
	Vhost           string // optional: vhost file to edit; detected otherwise
	MigrateOnDeploy bool
}

// MigratePlan is everything Migrate will do, computed without touching
// anything, so the caller can show it and ask for confirmation.
type MigratePlan struct {
	Opts       MigrateOptions
	Root       string // /var/www/<app>, the live directory today
	Vhost      string // real path of the vhost file that will be edited
	OldDocRoot string
	NewDocRoot string
	ReleaseID  string

	oldVhost, newVhost string
	vhostMode          os.FileMode
}

func (p *Provisioner) PlanMigrate(o MigrateOptions) (*MigratePlan, error) {
	if err := validate.AppName(o.App); err != nil {
		return nil, err
	}
	if err := validate.Type(o.Type); err != nil {
		return nil, err
	}
	root := p.Paths.AppDir(o.App)
	fi, err := os.Lstat(root)
	switch {
	case err != nil:
		return nil, fmt.Errorf("não encontrado: %s", root)
	case fi.Mode()&os.ModeSymlink != 0:
		return nil, fmt.Errorf("%s é um symlink — parece já migrado", root)
	case !fi.IsDir():
		return nil, fmt.Errorf("não é um diretório: %s", root)
	}
	for _, name := range []string{"releases", "current"} {
		if exists(filepath.Join(root, name)) {
			return nil, fmt.Errorf("%s/%s já existe — parece já migrado (veja `deploy-tools app status %s`)", root, name, o.App)
		}
	}

	candidates := []string{root, filepath.Join(root, "public")}
	vhost := o.Vhost
	if vhost == "" {
		found, err := apache.FindVhosts(p.Paths.SitesEnabled, candidates...)
		if err != nil {
			return nil, err
		}
		switch len(found) {
		case 0:
			return nil, fmt.Errorf("nenhum vhost ativo em %s com DocumentRoot %s ou %s/public — informe com --vhost", p.Paths.SitesEnabled, root, root)
		case 1:
			vhost = found[0]
		default:
			return nil, fmt.Errorf("mais de um vhost aponta para %s: %s — escolha com --vhost", root, strings.Join(found, ", "))
		}
	}
	if real, err := filepath.EvalSymlinks(vhost); err == nil {
		vhost = real
	}
	vfi, err := os.Stat(vhost)
	if err != nil {
		return nil, fmt.Errorf("vhost: %w", err)
	}
	content, err := os.ReadFile(vhost)
	if err != nil {
		return nil, err
	}

	var oldDoc string
	for _, d := range apache.DocumentRoots(string(content)) {
		for _, c := range candidates {
			if d == c {
				oldDoc = d
			}
		}
	}
	if oldDoc == "" {
		return nil, fmt.Errorf("%s não tem DocumentRoot %s nem %s/public", vhost, root, root)
	}
	newDoc := filepath.Join(root, "current", strings.TrimPrefix(oldDoc, root))
	newContent, _ := apache.Repoint(string(content), oldDoc, newDoc)

	return &MigratePlan{
		Opts: o, Root: root, Vhost: vhost, OldDocRoot: oldDoc, NewDocRoot: newDoc,
		ReleaseID: p.Now().Format("20060102150405") + "-pre-migracao",
		oldVhost:  string(content), newVhost: newContent, vhostMode: vfi.Mode().Perm(),
	}, nil
}

// Migrate converts a live plain directory into the releases/current layout
// with (practically) no downtime:
//
//  1. <root>/current -> . is created inside the legacy dir, so the final
//     DocumentRoot (<root>/current[/public]) already serves today's content;
//  2. the vhost is repointed there and Apache reloaded (configtest first,
//     undone on failure);
//  3. the slow chown -R runs while the site is still served in place;
//  4. the restructure itself is only renames on the same filesystem, so the
//     window where <root>/current is missing lasts milliseconds.
func (p *Provisioner) Migrate(pl *MigratePlan) error {
	root, app := pl.Root, pl.Opts.App
	legacyLink := filepath.Join(root, "current")

	// The vhost change is tested before the reload; the config must
	// already be valid, or that test would blame (and undo) the migration.
	if err := apache.ConfigTest(p.Runner); err != nil {
		return fmt.Errorf("%w: %w", apache.ErrConfigAlreadyBroken, err)
	}

	// 1 + 2: serve the same content through the final path.
	if err := os.Symlink(".", legacyLink); err != nil {
		return err
	}
	backup := fmt.Sprintf("%s.bak-%s", pl.Vhost, pl.ReleaseID)
	if err := os.WriteFile(backup, []byte(pl.oldVhost), pl.vhostMode); err != nil {
		_ = os.Remove(legacyLink)
		return err
	}
	if err := apache.WriteFileAtomic(pl.Vhost, []byte(pl.newVhost), pl.vhostMode); err != nil {
		_ = os.Remove(legacyLink)
		return err
	}
	err := apache.Apply(p.Runner, func() {
		_ = apache.WriteFileAtomic(pl.Vhost, []byte(pl.oldVhost), pl.vhostMode)
		_ = os.Remove(legacyLink)
	})
	if err != nil {
		return err
	}
	p.say("Site %s agora aponta para %s (backup em %s).", pl.Vhost, pl.NewDocRoot, backup)

	// 3: ownership, while still serving in place.
	if err := p.Owner.Chown(root, p.Paths.DeployUser, p.Paths.WebGroup, true); err != nil {
		return fmt.Errorf("chown: %w", err)
	}
	if writable := map[string][]string{
		validate.Laravel: {"storage", "bootstrap/cache"},
		validate.Symfony: {"var"},
	}[pl.Opts.Type]; len(writable) > 0 {
		// Apache (www-data) writes here; after the chown it is only the
		// group, so the group needs write access.
		for _, d := range writable {
			if err := groupWritable(filepath.Join(root, d)); err != nil {
				return err
			}
		}
	}

	// 4: the rename-only window.
	staging := filepath.Join(p.Paths.AppsRoot, fmt.Sprintf(".%s-migrating-%d", app, os.Getpid()))
	if err := p.restructure(pl, staging); err != nil {
		return fmt.Errorf("%w (o site continua servido pelo diretório original via %s)", err, legacyLink)
	}

	if err := appconf.Write(root, appconf.Conf{
		Type: pl.Opts.Type, Migrate: pl.Opts.MigrateOnDeploy && validate.IsPHP(pl.Opts.Type),
	}); err != nil {
		return err
	}
	return nil
}

func (p *Provisioner) restructure(pl *MigratePlan, staging string) (err error) {
	root := pl.Root
	finalRelease := filepath.Join(root, "releases", pl.ReleaseID)
	rel := filepath.Join(staging, "releases", pl.ReleaseID)
	shared := filepath.Join(staging, "shared")

	if err := p.mkdirShared(staging, filepath.Join(staging, "releases"), shared); err != nil {
		_ = os.RemoveAll(staging)
		return err
	}

	// Undo stack for a failure inside the window: put everything back
	// where it was, current -> . included, so the site keeps working.
	var undo []func()
	defer func() {
		if err != nil {
			for i := len(undo) - 1; i >= 0; i-- {
				undo[i]()
			}
			_ = os.RemoveAll(staging)
		}
	}()

	if err := os.Rename(root, rel); err != nil {
		return err
	}
	undo = append(undo, func() { _ = os.Rename(rel, root) })

	if err := os.Remove(filepath.Join(rel, "current")); err != nil {
		return err
	}
	undo = append(undo, func() { _ = os.Symlink(".", filepath.Join(rel, "current")) })

	moves := map[string][][2]string{ // release path -> shared/ entry
		validate.Laravel: {{".env", ".env"}, {"storage", "storage"}},
		validate.Symfony: {{".env.local", ".env.local"}, {"var/log", "var-log"}},
	}[pl.Opts.Type]
	if len(moves) > 0 {
		for _, mv := range moves {
			src, dst := filepath.Join(rel, mv[0]), filepath.Join(shared, mv[1])
			if !exists(src) {
				continue
			}
			if err := os.Rename(src, dst); err != nil {
				return err
			}
			undo = append(undo, func() { _ = os.Remove(src); _ = os.Rename(dst, src) })
		}
		if err := p.emptyEnv(filepath.Join(shared, release.EnvFile(pl.Opts.Type))); err != nil {
			return err
		}
	}
	if err := release.LinkShared(staging, rel, pl.Opts.Type); err != nil {
		return err
	}
	if err := os.Symlink(finalRelease, filepath.Join(staging, "current")); err != nil {
		return err
	}
	if err := os.Rename(staging, root); err != nil {
		return err
	}
	return nil
}

// Warnings lists things worth telling the operator after a migration.
func (pl *MigratePlan) Warnings() []string {
	var w []string
	if pl.Opts.Type == validate.Laravel {
		cached := filepath.Join(pl.Root, "current", "bootstrap", "cache", "config.php")
		if _, err := os.Stat(cached); err == nil {
			w = append(w, "Existe config em cache (bootstrap/cache/config.php) com caminhos antigos. "+
				"Rode como deploy: cd "+filepath.Join(pl.Root, "current")+" && php artisan config:cache")
		}
	}
	if validate.IsPHP(pl.Opts.Type) && !strings.HasSuffix(pl.OldDocRoot, "/public") {
		w = append(w, "O DocumentRoot antigo não terminava em /public, o que é incomum para "+
			map[string]string{validate.Laravel: "Laravel", validate.Symfony: "Symfony"}[pl.Opts.Type]+". Confira o tipo.")
	}
	return w
}

// groupWritable is chmod -R g+rwX (setgid on dirs) without following
// symlinks. Missing paths are ignored.
func groupWritable(root string) error {
	if !exists(root) {
		return nil
	}
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		mode := fi.Mode().Perm() | 0o060
		if d.IsDir() {
			return os.Chmod(path, mode|0o010|os.ModeSetgid)
		}
		if mode&0o100 != 0 {
			mode |= 0o010
		}
		return os.Chmod(path, mode)
	})
}
