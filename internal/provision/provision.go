// Package provision implements the manual, root-only operations: creating
// and migrating apps, installing the binary and managing the CI key.
package provision

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"time"

	"github.com/vinicgobbi/deploy-tools/internal/config"
	"github.com/vinicgobbi/deploy-tools/internal/release"
	"github.com/vinicgobbi/deploy-tools/internal/run"
)

// Owner changes file ownership. Real one uses os.Lchown; tests use a no-op.
type Owner interface {
	Chown(path, user, group string, recursive bool) error
}

type Provisioner struct {
	Paths  config.Paths
	Runner run.Runner
	Owner  Owner
	Out    io.Writer
	Now    func() time.Time
}

func New(p config.Paths, out io.Writer) *Provisioner {
	return &Provisioner{Paths: p, Runner: run.Exec{}, Owner: SystemOwner{}, Out: out, Now: time.Now}
}

func (p *Provisioner) Releases() *release.Manager {
	return &release.Manager{Paths: p.Paths, Runner: p.Runner, Out: p.Out, Err: p.Out}
}

// say logs a progress step, indented under the action's own messages.
func (p *Provisioner) say(format string, a ...any) { fmt.Fprintf(p.Out, "  · "+format+"\n", a...) }

// mkdirShared creates dirs with mode 2775 owned deploy:www-data. The setgid
// bit makes new files inherit www-data, so Apache can read what the deploy
// user writes.
func (p *Provisioner) mkdirShared(dirs ...string) error {
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o775); err != nil {
			return err
		}
		if err := os.Chmod(d, 0o775|os.ModeSetgid); err != nil {
			return err
		}
		if err := p.Owner.Chown(d, p.Paths.DeployUser, p.Paths.WebGroup, false); err != nil {
			return err
		}
	}
	return nil
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return !errors.Is(err, fs.ErrNotExist)
}

// SystemOwner chowns for real (by name lookup), never following symlinks.
type SystemOwner struct{}

func (SystemOwner) Chown(path, userName, groupName string, recursive bool) error {
	u, err := user.Lookup(userName)
	if err != nil {
		return err
	}
	g, err := user.LookupGroup(groupName)
	if err != nil {
		return err
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(g.Gid)
	if !recursive {
		return os.Lchown(path, uid, gid)
	}
	return filepath.WalkDir(path, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(p, uid, gid)
	})
}

// NoopOwner is used in tests.
type NoopOwner struct{}

func (NoopOwner) Chown(string, string, string, bool) error { return nil }
