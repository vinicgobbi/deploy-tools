// Package cli is the manual, human-facing side of deploy-tools: a cobra
// command tree plus an interactive menu (deploy-tools with no arguments).
// Both gather the same option structs and call the same actions, which in
// turn call internal/provision and internal/release.
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/vinicgobbi/deploy-tools/internal/clitext"
	"github.com/vinicgobbi/deploy-tools/internal/config"
	"github.com/vinicgobbi/deploy-tools/internal/provision"
	"github.com/vinicgobbi/deploy-tools/internal/ui"
	"github.com/vinicgobbi/deploy-tools/internal/update"
)

type Env struct {
	Paths   config.Paths
	P       *provision.Provisioner
	UI      *ui.UI
	Self    string // path of the running binary (for server install)
	Version string
	install *provision.InstallState // cached by installState()
	// TUI is set while the full-screen interface runs: actions there offer
	// copy buttons, since the menu beside the text breaks mouse selection.
	TUI       bool
	clipboard io.Writer // OSC 52 destination; nil = /dev/tty (tests replace it)

	// FromPATH: called by name, so it is the installed binary and the TUI
	// hides "Instalar/atualizar" (updates are automatic).
	FromPATH bool
	// Self-update (see selfupdate.go). Off in tests unless set.
	Updates     update.Client
	AutoUpdate  bool
	UpdateStamp string
	UpdateNote  string // shown in the TUI header after an automatic update
	// RequireRoot fails unless running as root; replaced in tests.
	RequireRoot func() error
}

func NewEnv(version string) *Env {
	u := ui.New()
	paths, err := config.Load(config.File)
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.ErrorText(err.Error()))
		os.Exit(1)
	}
	self, _ := os.Executable()
	return &Env{
		Paths: paths, P: provision.New(paths, u.Out), UI: u, Self: self, Version: version,
		FromPATH:    invokedFromPATH(os.Args[0]),
		AutoUpdate:  paths.AutoUpdate && os.Getenv("DEPLOY_TOOLS_NO_UPDATE") == "",
		UpdateStamp: updateStamp(),
		RequireRoot: func() error {
			if os.Geteuid() != 0 {
				return errors.New("precisa de root — rode com sudo")
			}
			return nil
		},
	}
}

func NewRoot(env *Env) *cobra.Command {
	root := &cobra.Command{
		Use:   "deploy-tools",
		Short: "Publica e gerencia sites no Apache, com deploy pelo GitHub Actions",
		Long: `deploy-tools publica e gerencia sites (Laravel, Symfony, React, Angular ou
estáticos) no Apache deste servidor. Cada push no GitHub vira uma versão
nova do site, com volta instantânea para a anterior.

Sem argumentos, num terminal, abre um menu com tudo isto e o próximo passo.

Por onde começar:
  sudo deploy-tools server setup         servidor novo
  sudo deploy-tools app create meu-site  primeiro app
  sudo deploy-tools                      menu interativo`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		// "version" is run by other copies (InstallState) and must stay
		// quick and quiet; "update" checks on its own.
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			switch cmd.Name() {
			case "version", "update", "ci", "help", "completion", "__complete", "__completeNoDesc":
				return
			}
			autoUpdate(env)
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !env.UI.Interactive {
				return cmd.Help()
			}
			return interactive(env)
		},
	}
	root.AddGroup(
		&cobra.Group{ID: "apps", Title: "Apps:"},
		&cobra.Group{ID: "server", Title: "Servidor:"},
		&cobra.Group{ID: "self", Title: "Esta ferramenta:"},
	)
	grouped(root, "apps", appCmd(env), releaseCmd(env))
	grouped(root, "server", serverCmd(env), keyCmd(env))
	grouped(root, "self", updateCmd(env))
	root.AddCommand(&cobra.Command{
		GroupID: "self",
		Use:     "version",
		Short:   "Mostra a versão",
		Args:    cobra.NoArgs,
		Run: func(*cobra.Command, []string) {
			// The first line is parsed by InstallState of other copies.
			env.UI.Println("deploy-tools", env.Version)
			printInstallSummary(env)
		},
	})
	root.AddCommand(&cobra.Command{
		Use:    "ci",
		Short:  "Modo forced command da CI (lê SSH_ORIGINAL_COMMAND)",
		Hidden: true,
		// Handled in main before cobra runs; reaching this means misuse.
		RunE: func(*cobra.Command, []string) error {
			return errors.New("o modo ci é tratado antes da CLI; chame o binário como deploy-ci")
		},
	})
	clitext.Localize(root)
	return root
}

// Execute runs the CLI and returns the exit code.
func Execute(env *Env, args []string) int {
	root := NewRoot(env)
	root.SetArgs(args)
	root.SetOut(env.UI.Out)
	if err := root.Execute(); err != nil {
		if errors.Is(err, ui.ErrAborted) {
			fmt.Fprintln(os.Stderr, "Cancelado.")
			return 130
		}
		fmt.Fprintln(os.Stderr, ui.ErrorText(clitext.Translate(err).Error()))
		return 1
	}
	return 0
}

// missing builds the error for a value that is required but was neither
// passed nor promptable (no TTY).
func missing(what, example string) error {
	return fmt.Errorf("%s não informado.\n  exemplo: %s", what, example)
}
