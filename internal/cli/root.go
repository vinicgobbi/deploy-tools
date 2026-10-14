// Package cli is the manual, human-facing side of deploy-tools: a cobra
// command tree plus an interactive menu (deploy-tools with no arguments).
// Both gather the same option structs and call the same actions, which in
// turn call internal/provision and internal/release.
package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/vinicgobbi/deploy-tools/internal/config"
	"github.com/vinicgobbi/deploy-tools/internal/provision"
	"github.com/vinicgobbi/deploy-tools/internal/ui"
)

type Env struct {
	Paths   config.Paths
	P       *provision.Provisioner
	UI      *ui.UI
	Self    string // path of the running binary (for server install)
	Version string
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
		Short: "Provisiona e gerencia apps no modelo releases/current",
		Long: `deploy-tools provisiona e gerencia apps servidos pelo Apache no modelo
releases/current, com deploy via GitHub Actions.

Sem argumentos, num terminal, abre um menu interativo.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !env.UI.Interactive {
				return cmd.Help()
			}
			return interactive(env)
		},
	}
	root.AddGroup(
		&cobra.Group{ID: "main", Title: "Comandos:"},
	)
	for _, c := range []*cobra.Command{appCmd(env), releaseCmd(env), keyCmd(env), serverCmd(env)} {
		c.GroupID = "main"
		root.AddCommand(c)
	}
	root.AddCommand(&cobra.Command{
		GroupID: "main",
		Use:     "version",
		Short:   "Mostra a versão",
		Args:    cobra.NoArgs,
		Run:     func(*cobra.Command, []string) { env.UI.Println("deploy-tools", env.Version) },
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
	root.SetHelpCommandGroupID("main")
	root.SetCompletionCommandGroupID("main")
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
		fmt.Fprintln(os.Stderr, ui.ErrorText(err.Error()))
		return 1
	}
	return 0
}

// missing builds the error for a value that is required but was neither
// passed nor promptable (no TTY).
func missing(what, example string) error {
	return fmt.Errorf("%s não informado.\n  exemplo: %s", what, example)
}
