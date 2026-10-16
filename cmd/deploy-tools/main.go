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
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/vinicgobbi/deploy-tools/internal/ci"
	"github.com/vinicgobbi/deploy-tools/internal/cli"
	"github.com/vinicgobbi/deploy-tools/internal/config"
	"github.com/vinicgobbi/deploy-tools/internal/release"
	"github.com/vinicgobbi/deploy-tools/internal/run"
)

var version = "dev"

func main() {
	if filepath.Base(os.Args[0]) == "deploy-ci" || (len(os.Args) == 2 && os.Args[1] == "ci") {
		// When the CI connection drops, sshd hangs up the forced command. A
		// deploy that already has its tarball must still finish (or clean up)
		// on its own instead of dying halfway; the action then sees it in
		// "list". A truncated upload still fails in tar and is removed.
		signal.Ignore(syscall.SIGHUP, syscall.SIGPIPE)
		m := &release.Manager{Paths: config.Default(), Runner: run.Exec{}, Out: os.Stdout, Err: os.Stderr}
		os.Exit(ci.Main(m, os.Getenv("SSH_ORIGINAL_COMMAND"), os.Stdin))
	}
	os.Exit(cli.Execute(cli.NewEnv(version), os.Args[1:]))
}
