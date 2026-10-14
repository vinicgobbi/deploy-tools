//go:build !windows

package devcli

import (
	"errors"
	"os/exec"
)

// addToUserPath is only used on Windows; unix shells use ShellRC.
func addToUserPath(string) (bool, error) {
	return false, errors.New("PATH do usuário via registro só existe no Windows")
}

// clearQuarantine removes the macOS quarantine flag browsers set on
// downloads, which would otherwise block the first run. Best effort.
func clearQuarantine(path string) {
	_ = exec.Command("xattr", "-d", "com.apple.quarantine", path).Run()
}
