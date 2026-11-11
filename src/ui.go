package main

import (
	"fmt"

	"github.com/jesseduffield/gocui"
)

const mainView = "main"

// layout draws the single framed view that fills the terminal.
func layout(g *gocui.Gui) error {
	w, h := g.Size()
	v, err := g.SetView(mainView, 0, 0, w-1, h-2, 0)
	if err != nil && err.Error() != gocui.ErrUnknownView.Error() {
		return err
	}
	v.Title = "lazyslurm"
	v.FrameRunes = []rune{'─', '│', '╭', '╮', '╰', '╯'}
	v.Clear()
	fmt.Fprintln(v, "Hello, lazyslurm. Press q to quit.")
	_, err = g.SetCurrentView(mainView)
	return err
}

// bind sets the keys: q and ctrl-c quit.
func bind(g *gocui.Gui) error {
	for _, key := range []any{'q', gocui.KeyCtrlC} {
		if err := g.SetKeybinding("", key, gocui.ModNone, quit); err != nil {
			return err
		}
	}
	return nil
}

func quit(*gocui.Gui, *gocui.View) error {
	return gocui.ErrQuit
}
