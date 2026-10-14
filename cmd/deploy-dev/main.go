// Command deploy-dev is the developer-desktop side of deploy-tools: it
// generates and checks a project's deploy workflow. It deliberately links
// none of the server code (see cmd/deploy-tools for that).
package main

import (
	"os"

	"github.com/vinicgobbi/deploy-tools/internal/devcli"
	"github.com/vinicgobbi/deploy-tools/internal/ui"
)

// Set at build time (see Makefile): the release version and the full SHA
// of the commit it was built from, which init pins the deploy action to.
var (
	version = "dev"
	commit  = ""
)

func main() {
	os.Exit(devcli.Execute(&devcli.Env{UI: ui.New(), Version: version, Commit: commit}, os.Args[1:]))
}
