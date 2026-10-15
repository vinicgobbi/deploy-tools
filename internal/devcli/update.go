package devcli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/vinicgobbi/deploy-tools/internal/update"
)

const (
	autoCheckEvery   = 24 * time.Hour
	autoCheckTimeout = 4 * time.Second
	downloadTimeout  = 2 * time.Minute
)

// InvokedFromPATH: called by name (deploy-dev), not by a path
// (./deploy-dev-linux-amd64).
func InvokedFromPATH(arg0 string) bool { return filepath.Base(arg0) == arg0 }

// UpdateStamp is where the last automatic check is recorded.
func UpdateStamp() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "deploy-dev", "update-check")
}

var errNotInstalled = errors.New("deploy-dev não está instalado no PATH do usuário — rode ./deploy-dev-<sistema> install primeiro")

// installedPath is the per-user installed binary, "" when there is none.
func installedPath() (*Installer, string) {
	in, err := newInstaller()
	if err != nil {
		return nil, ""
	}
	p := filepath.Join(in.DefaultDir(), in.binName())
	if _, err := os.Stat(p); err != nil {
		return in, ""
	}
	return in, p
}

// applyUpdate downloads release r and puts it over the installed binary
// (on Windows the running .exe is moved aside, see Installer.Copy).
func applyUpdate(env *Env, r update.Release) error {
	in, dst := installedPath()
	if dst == "" {
		return errNotInstalled
	}
	ctx, cancel := context.WithTimeout(context.Background(), downloadTimeout)
	defer cancel()
	tmp, err := env.Updates.Download(ctx, r, update.AssetName("deploy-dev", runtime.GOOS, runtime.GOARCH), filepath.Dir(dst))
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	in.Self = tmp
	if _, _, err := in.Copy(filepath.Dir(dst)); err != nil {
		return err
	}
	if in.GOOS == "darwin" {
		clearQuarantine(dst)
	}
	return nil
}

// autoUpdate runs before commands: at most once a day it asks GitHub for a
// newer release and updates the installed deploy-dev (it takes effect on
// the next run). Failures are quiet.
func autoUpdate(env *Env) {
	now := time.Now()
	if !env.AutoUpdate || !update.Updatable(env.Version) || !update.Due(env.UpdateStamp, autoCheckEvery, now) {
		return
	}
	_ = update.Touch(env.UpdateStamp, now)
	ctx, cancel := context.WithTimeout(context.Background(), autoCheckTimeout)
	defer cancel()
	r, err := env.Updates.Latest(ctx)
	if err != nil || !update.Newer(env.Version, r.Version) {
		return
	}
	if err := applyUpdate(env, r); err != nil {
		env.UI.Hint("deploy-dev %s disponível (este é %s): deploy-dev update", r.Version, env.Version)
		return
	}
	env.UI.OK("deploy-dev atualizado: %s → %s (vale a partir da próxima execução).", env.Version, r.Version)
}

func updateCmd(env *Env) *cobra.Command {
	var check bool
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Atualiza o deploy-dev instalado para a última release",
		Long: `Baixa a última release do seu sistema, confere o SHA-256 publicado com
ela e substitui o deploy-dev instalado no PATH do usuário.

A verificação também é automática, no máximo uma vez por dia. Para
desligar: DEPLOY_DEV_NO_UPDATE=1.`,
		Example: "  deploy-dev update\n  deploy-dev update --check",
		Args:    cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			r, err := env.Updates.Latest(ctx)
			if err != nil {
				return err
			}
			if !update.Newer(env.Version, r.Version) {
				env.UI.OK("Já está na versão mais recente (%s; última release: %s).", env.Version, r.Version)
				return nil
			}
			if check {
				env.UI.Println("Nova versão: " + r.Version + " (esta é " + env.Version + "). Atualize com: deploy-dev update")
				return nil
			}
			if err := applyUpdate(env, r); err != nil {
				return err
			}
			env.UI.OK("deploy-dev atualizado: %s → %s.", env.Version, r.Version)
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "só verifica, sem instalar")
	return cmd
}
