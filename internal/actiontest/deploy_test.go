// Package actiontest runs action/deploy.sh (the GitHub Action's script)
// against a fake ssh, to check the retry behavior without a server.
package actiontest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeSSH logs every call and answers by MODE:
//
//	flaky   the first FAILS deploys time out (255), then they work
//	drop    the deploy works but the connection drops at the end (255)
//	refuse  deploy-ci refuses (exit 1)
//	down    every connection times out
const fakeSSH = `#!/bin/bash
cmd="${@: -1}"
echo "$cmd" >> "$FAKE_DIR/calls"
case "$cmd" in
  list*)
    [ "$MODE" = down ] && exit 255
    [ -f "$FAKE_DIR/current" ] && echo "* $(cat "$FAKE_DIR/current") (current)"
    exit 0 ;;
  deploy*)
    id="${cmd##* }"
    cat > /dev/null
    case "$MODE" in
      down) echo "ssh: connect to host x port 22: Connection timed out" >&2; exit 255 ;;
      refuse) echo "error: unknown app: x" >&2; exit 1 ;;
      drop) echo "$id" > "$FAKE_DIR/current"; exit 255 ;;
      flaky)
        n=$(grep -c '^deploy' "$FAKE_DIR/calls")
        if [ "$n" -le "$FAILS" ]; then echo "ssh: connect to host x port 22: Connection timed out" >&2; exit 255; fi
        echo "$id" > "$FAKE_DIR/current"; echo "deployed x -> $id"; exit 0 ;;
    esac ;;
esac
exit 255
`

func run(t *testing.T, mode string, env ...string) (out string, code int, calls []string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	_ = os.MkdirAll(bin, 0o755)
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(fakeSSH), 0o755); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(dir, "release.tar.gz")
	_ = os.WriteFile(archive, []byte("tar"), 0o644)
	script, _ := filepath.Abs("../../action/deploy.sh")
	cmd := exec.Command("bash", script)
	cmd.Env = append(os.Environ(),
		"PATH="+bin+":"+os.Getenv("PATH"), "FAKE_DIR="+dir, "MODE="+mode,
		"APP=site", "ARCHIVE="+archive, "SUFFIX=v1.0.0", "GITHUB_SHA=abcdef0123",
		"DEPLOY_HOST=srv", "DEPLOY_USER=deploy", "DEPLOY_SSH_KEY=k", "DEPLOY_HOST_KEY=srv ssh-ed25519 AAAA",
		"RETRY_DELAY=0", "GITHUB_OUTPUT="+filepath.Join(dir, "output"),
	)
	cmd.Env = append(cmd.Env, env...)
	b, err := cmd.CombinedOutput()
	code = 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	c, _ := os.ReadFile(filepath.Join(dir, "calls"))
	return string(b), code, strings.Split(strings.TrimSpace(string(c)), "\n")
}

func count(calls []string, prefix string) int {
	n := 0
	for _, c := range calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func TestRetriesUntilTheConnectionComesBack(t *testing.T) {
	out, code, calls := run(t, "flaky", "FAILS=2")
	if code != 0 || count(calls, "deploy") != 3 || !strings.Contains(out, "tentativa 2 de 6") || !strings.Contains(out, "Publicado site ->") {
		t.Fatalf("code %d, calls %v\n%s", code, calls, out)
	}
}

func TestConnectionDroppedAfterTheDeployCountsAsSuccess(t *testing.T) {
	out, code, calls := run(t, "drop")
	if code != 0 || count(calls, "deploy") != 1 || !strings.Contains(out, "chegou e está no ar") {
		t.Fatalf("must not deploy twice: code %d, calls %v\n%s", code, calls, out)
	}
}

func TestServerRefusalIsNotRetried(t *testing.T) {
	out, code, calls := run(t, "refuse")
	if code != 1 || count(calls, "deploy") != 1 || !strings.Contains(out, "o servidor recusou o deploy") {
		t.Fatalf("code %d, calls %v\n%s", code, calls, out)
	}
}

func TestGivesUpAfterAttempts(t *testing.T) {
	out, code, calls := run(t, "down", "ATTEMPTS=3")
	if code != 255 || count(calls, "deploy") != 3 || !strings.Contains(out, "depois de 3 tentativas") {
		t.Fatalf("code %d, calls %v\n%s", code, calls, out)
	}
}

func TestEachAttemptUsesAFreshReleaseID(t *testing.T) {
	_, _, calls := run(t, "down", "ATTEMPTS=2", "RETRY_DELAY=1")
	var ids []string
	for _, c := range calls {
		if strings.HasPrefix(c, "deploy site ") {
			ids = append(ids, strings.TrimPrefix(c, "deploy site "))
		}
	}
	if len(ids) != 2 || ids[0] == ids[1] || !strings.HasSuffix(ids[0], "-v1.0.0") {
		t.Fatalf("ids = %v", ids)
	}
}
