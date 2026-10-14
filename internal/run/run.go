// Package run abstracts external commands (tar, php, apache2ctl, ...) so
// tests can record and fake them.
package run

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

type Cmd struct {
	Dir    string
	Name   string
	Args   []string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

func (c Cmd) String() string { return strings.TrimSpace(c.Name + " " + strings.Join(c.Args, " ")) }

type Runner interface {
	Run(c Cmd) error
}

// Exec runs commands for real.
type Exec struct{}

func (Exec) Run(c Cmd) error {
	cmd := exec.Command(c.Name, c.Args...)
	cmd.Dir, cmd.Stdin, cmd.Stdout, cmd.Stderr = c.Dir, c.Stdin, c.Stdout, c.Stderr
	return cmd.Run()
}

// Output runs c capturing stdout+stderr, returning them in the error on
// failure so callers can show what went wrong (e.g. apache2ctl configtest).
func Output(r Runner, c Cmd) (string, error) {
	var buf bytes.Buffer
	c.Stdout, c.Stderr = &buf, &buf
	err := r.Run(c)
	out := buf.String()
	if err != nil {
		return out, fmt.Errorf("%s: %w\n%s", c, err, strings.TrimSpace(out))
	}
	return out, nil
}
