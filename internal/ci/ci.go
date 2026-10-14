// Package ci is the forced command behind the shared "deploy" SSH key.
// sshd puts whatever command the client asked for in SSH_ORIGINAL_COMMAND;
// the authorized_keys "command=" made sshd run us instead, so that string is
// the only client input. Anything not explicitly recognized is refused, so
// a leaked CI key can only deploy/rollback/list under /var/www/<app>.
//
// Output and error texts are kept identical to the original bash wrapper so
// existing pipelines keep working unchanged.
package ci

import (
	"fmt"
	"io"
	"strings"

	"github.com/vinicgobbi/deploy-tools/internal/release"
)

const usage = "usage: deploy <app> <release_id> | rollback <app> <release_id> | list <app>"

// Main runs one CI request and returns the process exit code.
func Main(m *release.Manager, sshCommand string, stdin io.Reader) int {
	args := strings.Fields(sshCommand)
	arg := func(i int) string {
		if i < len(args) {
			return args[i]
		}
		return ""
	}

	var err error
	switch arg(0) {
	case "deploy":
		err = m.Deploy(arg(1), arg(2), stdin)
	case "rollback":
		err = m.Rollback(arg(1), arg(2))
	case "list":
		var rels []release.Release
		rels, err = m.List(arg(1))
		for _, r := range rels {
			if r.Current {
				fmt.Fprintf(m.Out, "* %s (current)\n", r.ID)
			} else {
				fmt.Fprintf(m.Out, "  %s\n", r.ID)
			}
		}
	default:
		err = fmt.Errorf("%s", usage)
	}
	if err != nil {
		fmt.Fprintf(m.Err, "error: %s\n", err)
		return 1
	}
	return 0
}
