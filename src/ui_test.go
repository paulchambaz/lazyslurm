package main

import (
	"strings"
	"testing"

	"github.com/jesseduffield/gocui"
)

func TestLayoutSaysHello(t *testing.T) {
	g, err := gocui.NewGui(gocui.NewGuiOpts{Headless: true, Width: 40, Height: 10})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	if err := layout(g); err != nil {
		t.Fatal(err)
	}
	v, err := g.View(mainView)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(v.Buffer(), "Hello, lazyslurm") {
		t.Errorf("main view = %q", v.Buffer())
	}
	if g.CurrentView() != v {
		t.Error("main view has no focus, so q cannot quit")
	}
	if len(v.FrameRunes) != 6 || v.FrameRunes[2] != '╭' {
		t.Errorf("frame = %q, want rounded corners", string(v.FrameRunes))
	}
}

func TestQuitStopsTheLoop(t *testing.T) {
	if err := quit(nil, nil); err != gocui.ErrQuit {
		t.Errorf("quit = %v, want ErrQuit", err)
	}
}
