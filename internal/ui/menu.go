package ui

import (
	"errors"

	"github.com/charmbracelet/huh"
)

// Entry is one menu option. Without Run it leaves the menu (back/exit);
// with Screen set, Run opens a screen of its own instead of an action.
type Entry struct {
	Label  string
	Title  string // heading of the action's output (default: Label)
	Run    func() error
	Screen bool
}

// Menu loops over a screen until an entry without Run is picked (or the
// menu is aborted). Entering the screen clears it and draws header; after
// an action, its output stays visible and the menu reappears below it, so
// results can be read without an extra "back" step. entries is called on
// every round, so the options follow what the last action changed.
func (u *UI) Menu(header func() error, title string, entries func() ([]Entry, error)) error {
	fresh := true
	for {
		if fresh {
			u.Clear()
			if err := header(); err != nil {
				return err
			}
		}
		es, err := entries()
		if err != nil {
			return err
		}
		opts := make([]huh.Option[int], len(es))
		for i, e := range es {
			opts[i] = huh.NewOption(e.Label, i)
		}
		choice := 0
		if err := u.Run(huh.NewGroup(
			huh.NewSelect[int]().Title(title).Options(opts...).Value(&choice),
		)); err != nil {
			if errors.Is(err, ErrAborted) {
				return nil
			}
			return err
		}

		e := es[choice]
		switch {
		case e.Run == nil:
			return nil
		case e.Screen:
			err = e.Run()
			fresh = err == nil
		default:
			u.Clear()
			t := e.Title
			if t == "" {
				t = e.Label
			}
			u.Title(t)
			u.Println()
			err = e.Run()
			fresh = false
		}
		switch {
		case errors.Is(err, ErrAborted):
			u.Println("Cancelado.")
		case err != nil:
			u.Println(ErrorText(err.Error()))
		}
		if !fresh {
			u.Println()
		}
	}
}
