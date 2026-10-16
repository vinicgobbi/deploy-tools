package cli

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/vinicgobbi/deploy-tools/internal/update"
)

// How often the automatic check may reach GitHub, and how long it may
// delay the command the operator actually ran.
const (
	autoCheckEvery   = 24 * time.Hour
	autoCheckTimeout = 4 * time.Second
	downloadTimeout  = 2 * time.Minute
)

// invokedFromPATH: the binary was called by name (deploy-tools, sudo
// deploy-tools), not by a path (./deploy-tools-linux-amd64, /tmp/...).
func invokedFromPATH(arg0 string) bool { return filepath.Base(arg0) == arg0 }

// updateStamp is where the last automatic check is recorded.
func updateStamp() string {
	if os.Geteuid() == 0 {
		return "/var/cache/deploy-tools/update-check"
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "deploy-tools", "update-check")
}

// applyUpdate downloads release r and installs it like `server install`
// (binary + deploy-ci symlink, both swapped atomically).
func applyUpdate(env *Env, r update.Release) error {
	ctx, cancel := context.WithTimeout(context.Background(), downloadTimeout)
	defer cancel()
	tmp, err := env.Updates.Download(ctx, r, update.AssetName("deploy-tools", runtime.GOOS, runtime.GOARCH), env.Paths.BinDir)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	if err := env.P.Install(tmp); err != nil {
		return err
	}
	env.install = nil
	return nil
}

// autoUpdate runs before CLI commands: at most once a day it asks GitHub
// for a newer release and, as root with the binary installed, installs it
// (it takes effect on the next run). Otherwise it only tells. Any failure
// is quiet: the command the operator ran always goes on.
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
	if env.RequireRoot() != nil || !env.installState().Installed {
		env.UI.Hint("deploy-tools %s disponível (este é %s): sudo deploy-tools update", r.Version, env.Version)
		return
	}
	if err := applyUpdate(env, r); err != nil {
		env.UI.Warn("Atualização automática para %s falhou: %v — rode sudo deploy-tools update", r.Version, err)
		return
	}
	env.UpdateNote = "atualizado para " + r.Version + " (vale na próxima execução)"
	env.UI.OK("deploy-tools atualizado: %s → %s. A próxima execução e os próximos deploys da CI já usam a versão nova.",
		env.Version, r.Version)
	showChanges(env, env.Version, r.Version, autoNotesLines)
}

// autoNotesLines caps the notes after an automatic update: they show up
// before the command the operator actually ran.
const autoNotesLines = 8

// showChanges prints the changelog from current to latest; it never fails.
func showChanges(env *Env, current, latest string, maxLines int) {
	ctx, cancel := context.WithTimeout(context.Background(), autoCheckTimeout)
	defer cancel()
	env.Updates.ShowChanges(ctx, env.UI, current, latest, maxLines)
}

func updateCmd(env *Env) *cobra.Command {
	var check bool
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Verifica e instala a versão mais nova",
		Long: `Mostra o que mudou desde a sua versão (o changelog de cada release),
baixa a última release, confere o SHA-256 publicado com ela e instala
como o "server install" (troca atômica do binário e do deploy-ci).

A verificação também é automática: no máximo uma vez por dia, os comandos
consultam o GitHub e, como root, instalam a versão nova (vale a partir da
próxima execução). Para desligar: AUTO_UPDATE=no em ` + "/etc/deploy-tools.conf" + `
ou a variável DEPLOY_TOOLS_NO_UPDATE=1. O deploy da CI nunca consulta nada.`,
		Example: "  sudo deploy-tools update\n  deploy-tools update --check",
		Args:    cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if !check {
				if err := env.RequireRoot(); err != nil {
					return err
				}
			}
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
			showChanges(env, env.Version, r.Version, 0)
			if check {
				env.UI.Println("Nova versão: " + r.Version + " (esta é " + env.Version + "). Atualize com: sudo deploy-tools update")
				return nil
			}
			if err := applyUpdate(env, r); err != nil {
				return err
			}
			env.UI.OK("deploy-tools atualizado: %s → %s. Os próximos deploys da CI já usam a versão nova.", env.Version, r.Version)
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "só verifica, sem instalar")
	return cmd
}

// iCheckUpdate is the TUI's "Verificar atualizações".
func iCheckUpdate(env *Env) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	r, err := env.Updates.Latest(ctx)
	if err != nil {
		return err
	}
	env.UI.KV([2]string{"esta versão", env.Version}, [2]string{"última release", r.Version})
	env.UI.Println()
	if !update.Newer(env.Version, r.Version) {
		env.UI.OK("Está na versão mais recente.")
		return nil
	}
	showChanges(env, env.Version, r.Version, 0)
	if env.RequireRoot() != nil {
		env.UI.Hint("Para atualizar: sudo deploy-tools update")
		return nil
	}
	if !env.installState().Installed {
		env.UI.Hint("O deploy-tools ainda não está instalado neste servidor: use \"Preparar servidor\" primeiro.")
		return nil
	}
	ok, err := env.UI.ConfirmDefault("Atualizar agora para "+r.Version+"?", "Troca atômica do binário; vale a partir da próxima execução.", true)
	if err != nil || !ok {
		return err
	}
	if err := applyUpdate(env, r); err != nil {
		return err
	}
	env.UpdateNote = "atualizado para " + r.Version + " (vale na próxima execução)"
	env.UI.OK("Atualizado para %s. Saia e abra de novo para usar a versão nova.", r.Version)
	return nil
}

// autoUpdateState describes the automatic update setting, for the TUI.
func autoUpdateState(env *Env) string {
	if env.AutoUpdate {
		return "Atualização automática: ligada (verifica no máximo uma vez por dia; desligue com AUTO_UPDATE=no em /etc/deploy-tools.conf)."
	}
	return "Atualização automática: desligada."
}
