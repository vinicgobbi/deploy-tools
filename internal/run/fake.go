package run

import (
	"errors"
	"io"
	"strings"
	"sync"
)

// Fake records every command. Commands whose String() starts with a key in
// Fail return that error; keys in Output get that text written to stdout.
// Func, when set, sees every command first and can fail it (for failures
// that depend on earlier commands). Everything else is passed to
// Passthrough when set (so tests can still use the real tar), or succeeds
// silently.
type Fake struct {
	mu          sync.Mutex
	Calls       []Cmd
	Fail        map[string]error
	Output      map[string]string
	Func        func(Cmd) error
	Passthrough map[string]bool // command names delegated to Exec
}

var ErrFake = errors.New("fake failure")

func (f *Fake) Run(c Cmd) error {
	f.mu.Lock()
	f.Calls = append(f.Calls, c)
	f.mu.Unlock()
	s := c.String()
	if f.Func != nil {
		if err := f.Func(c); err != nil {
			return err
		}
	}
	for prefix, out := range f.Output {
		if strings.HasPrefix(s, prefix) && c.Stdout != nil {
			_, _ = io.WriteString(c.Stdout, out)
		}
	}
	for prefix, err := range f.Fail {
		if strings.HasPrefix(s, prefix) {
			return err
		}
	}
	if f.Passthrough[c.Name] {
		return Exec{}.Run(c)
	}
	return nil
}

// Strings returns the recorded commands as "name args" lines.
func (f *Fake) Strings() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.Calls))
	for _, c := range f.Calls {
		out = append(out, c.String())
	}
	return out
}
