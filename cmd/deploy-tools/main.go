// Command deploy-tools has two faces:
//
//   - invoked as "deploy-ci" (the symlink every authorized_keys
//     command= points at) or as "deploy-tools ci", it is the CI forced
//     command: it reads only SSH_ORIGINAL_COMMAND, with fixed paths;
//   - otherwise it is the manual CLI for operators (see internal/cli).
//
// The CI path is dispatched before any CLI/TUI code runs.
package main

import (
	"os"
	"path/filepath"

	"github.com/vinicgobbi/deploy-tools/internal/ci"
	"github.com/vinicgobbi/deploy-tools/internal/cli"
	"github.com/vinicgobbi/deploy-tools/internal/config"
	"github.com/vinicgobbi/deploy-tools/internal/release"
	"github.com/vinicgobbi/deploy-tools/internal/run"
)

var version = "dev"

func main() {
	if filepath.Base(os.Args[0]) == "deploy-ci" || (len(os.Args) == 2 && os.Args[1] == "ci") {
		m := &release.Manager{Paths: config.Default(), Runner: run.Exec{}, Out: os.Stdout, Err: os.Stderr}
		os.Exit(ci.Main(m, os.Getenv("SSH_ORIGINAL_COMMAND"), os.Stdin))
	}
	os.Exit(cli.Execute(cli.NewEnv(version), os.Args[1:]))
}
