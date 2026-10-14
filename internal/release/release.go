// Package release implements the releases/current layout:
//
//	/var/www/<app>/
//	├── current -> /var/www/<app>/releases/<id>
//	├── releases/<id>/...
//	└── shared/            (symlinked into every release)
//
// It is used both by the CI forced command and by the manual CLI. Messages
// written to Out and error texts are part of the CI protocol and match the
// original bash forced command byte for byte (see internal/ci tests).
package release

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"syscall"

	"github.com/vinicgobbi/deploy-tools/internal/appconf"
	"github.com/vinicgobbi/deploy-tools/internal/config"
	"github.com/vinicgobbi/deploy-tools/internal/run"
	"github.com/vinicgobbi/deploy-tools/internal/validate"
)

var (
	ErrUnknownApp      = errors.New("unknown app")
	ErrReleaseExists   = errors.New("release already exists")
	ErrReleaseNotFound = errors.New("release not found (was it pruned?)")
)

type Manager struct {
	Paths  config.Paths
	Runner run.Runner
	Out    io.Writer // progress/result messages (stdout in CI mode)
	Err    io.Writer // output of hooks/tar (stderr in CI mode)
}

type Release struct {
	ID      string
	Current bool
}

// ValidateApp checks the name and that /var/www/<app> already exists, so
// the CI key can never create directories outside the convention.
func (m *Manager) ValidateApp(app string) error {
	if err := validate.AppName(app); err != nil {
		return err
	}
	if fi, err := os.Stat(m.Paths.AppDir(app)); err != nil || !fi.IsDir() {
		return fmt.Errorf("%w: %s (create %s first)", ErrUnknownApp, app, m.Paths.AppDir(app))
	}
	return nil
}

// Deploy extracts a gzipped tarball into releases/<id>, links shared/, runs
// the per-type hooks and only then swaps current. Any failure before the
// swap removes the half-built release, leaving current untouched and the id
// free for a retry.
func (m *Manager) Deploy(app, id string, tarball io.Reader) (err error) {
	if err := m.ValidateApp(app); err != nil {
		return err
	}
	if err := validate.ReleaseID(id); err != nil {
		return err
	}
	unlock, err := m.lock(app)
	if err != nil {
		return err
	}
	defer unlock()

	releasesDir := m.Paths.ReleasesDir(app)
	target := filepath.Join(releasesDir, id)
	if err := os.MkdirAll(releasesDir, 0o775); err != nil {
		return err
	}
	if _, err := os.Lstat(target); err == nil {
		return fmt.Errorf("%w: %s", ErrReleaseExists, id)
	}
	if err := os.Mkdir(target, 0o775); err != nil {
		return err
	}
	swapped := false
	defer func() {
		if !swapped {
			_ = os.RemoveAll(target)
		}
	}()

	if err := m.Runner.Run(run.Cmd{
		Name: "tar", Args: []string{"-xzf", "-", "-C", target},
		Stdin: tarball, Stdout: m.Err, Stderr: m.Err,
	}); err != nil {
		return fmt.Errorf("extracting tarball: %w", err)
	}

	if err := LinkShared(m.Paths.AppDir(app), target); err != nil {
		return fmt.Errorf("linking shared/: %w", err)
	}

	conf, ok, err := appconf.Read(m.Paths.AppDir(app))
	if err != nil {
		return err
	}
	if ok {
		if err := m.runHooks(conf, target); err != nil {
			return err
		}
	}

	if err := SwapCurrent(m.Paths.AppDir(app), target); err != nil {
		return err
	}
	swapped = true

	if err := m.Prune(app); err != nil {
		return err
	}
	fmt.Fprintf(m.Out, "deployed %s -> %s\n", app, id)
	return nil
}

// LaravelHooks are fixed: nothing shipped in the tarball is ever executed as
// a script by this tool. (artisan itself is app code, which the web server
// would run anyway.)
func LaravelHooks(c appconf.Conf) [][]string {
	hooks := [][]string{
		{"artisan", "storage:link"},
		{"artisan", "config:cache"},
		{"artisan", "route:cache"},
		{"artisan", "view:cache"},
	}
	if c.LaravelMigrate {
		hooks = append(hooks, []string{"artisan", "migrate", "--force"})
	}
	return hooks
}

func (m *Manager) runHooks(c appconf.Conf, releaseDir string) error {
	if c.Type != validate.Laravel {
		return nil
	}
	for _, args := range LaravelHooks(c) {
		cmd := run.Cmd{Dir: releaseDir, Name: "php", Args: args, Stdout: m.Err, Stderr: m.Err}
		if err := m.Runner.Run(cmd); err != nil {
			return fmt.Errorf("hook failed (%s): %w", cmd, err)
		}
	}
	return nil
}

func (m *Manager) Rollback(app, id string) error {
	if err := m.ValidateApp(app); err != nil {
		return err
	}
	if err := validate.ReleaseID(id); err != nil {
		return err
	}
	unlock, err := m.lock(app)
	if err != nil {
		return err
	}
	defer unlock()

	target := filepath.Join(m.Paths.ReleasesDir(app), id)
	if fi, err := os.Stat(target); err != nil || !fi.IsDir() {
		return fmt.Errorf("%w: %s", ErrReleaseNotFound, id)
	}
	if err := SwapCurrent(m.Paths.AppDir(app), target); err != nil {
		return err
	}
	fmt.Fprintf(m.Out, "rolled back %s -> %s\n", app, id)
	return nil
}

// List returns releases newest first. Ids start with a timestamp, so name
// order is deploy order — unlike mtime, which tar extraction and copies can
// disturb.
func (m *Manager) List(app string) ([]Release, error) {
	if err := m.ValidateApp(app); err != nil {
		return nil, err
	}
	ids, err := sortedIDs(m.Paths.ReleasesDir(app))
	if err != nil {
		return nil, err
	}
	cur := m.CurrentID(app)
	out := make([]Release, len(ids))
	for i, id := range ids {
		out[i] = Release{ID: id, Current: id == cur}
	}
	return out, nil
}

// CurrentID returns the release id current points to, or "" if none.
func (m *Manager) CurrentID(app string) string {
	cur, err := filepath.EvalSymlinks(m.Paths.CurrentLink(app))
	if err != nil {
		return ""
	}
	rel, err := filepath.EvalSymlinks(m.Paths.ReleasesDir(app))
	if err != nil || filepath.Dir(cur) != rel {
		return ""
	}
	return filepath.Base(cur)
}

// Prune keeps the KeepReleases newest releases, never counting or removing
// whatever current points to (so after a rollback to an old release, that
// one plus the KeepReleases-1 newest others survive).
func (m *Manager) Prune(app string) error {
	ids, err := sortedIDs(m.Paths.ReleasesDir(app))
	if err != nil {
		return err
	}
	cur := m.CurrentID(app)
	kept := 0
	for _, id := range ids {
		if id == cur {
			continue
		}
		kept++
		if kept >= m.Paths.KeepReleases {
			fmt.Fprintf(m.Out, "pruning old release: %s/%s\n", app, id)
			if err := os.RemoveAll(filepath.Join(m.Paths.ReleasesDir(app), id)); err != nil {
				return err
			}
		}
	}
	return nil
}

func sortedIDs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	return ids, nil
}

// SwapCurrent atomically repoints <appDir>/current: the new link is built
// next to the old one and renamed over it, so requests never see a missing
// current.
func SwapCurrent(appDir, target string) error {
	tmp := filepath.Join(appDir, fmt.Sprintf("current.tmp.%d", os.Getpid()))
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(appDir, "current")); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// LinkShared symlinks every entry of <appDir>/shared (dotfiles included, so
// .env is covered) into releaseDir, replacing whatever the tarball shipped
// in that slot (e.g. an empty storage/ stub from the git checkout).
func LinkShared(appDir, releaseDir string) error {
	entries, err := os.ReadDir(filepath.Join(appDir, "shared"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		dst := filepath.Join(releaseDir, e.Name())
		if err := os.RemoveAll(dst); err != nil {
			return err
		}
		if err := os.Symlink(filepath.Join("..", "..", "shared", e.Name()), dst); err != nil {
			return err
		}
	}
	return nil
}

// lock serializes deploys/rollbacks of the same app (two pushes in a row
// would otherwise race on current and on pruning). Blocks until free.
func (m *Manager) lock(app string) (func(), error) {
	// Read-only is enough for flock, and keeps working when the file was
	// created by root (manual rollback) with a umask-reduced mode.
	f, err := os.OpenFile(filepath.Join(m.Paths.AppDir(app), ".deploy.lock"), os.O_CREATE|os.O_RDONLY, 0o664)
	if err != nil {
		return nil, fmt.Errorf("lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("lock: %w", err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
