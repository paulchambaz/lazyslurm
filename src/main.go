// Command lazyslurm is a terminal UI for Slurm clusters, opened from a repo.
package main

import (
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/jesseduffield/gocui"
)

//go:embed VERSION
var version string

const usage = `usage: lazyslurm [-hV]

A terminal UI for Slurm clusters, opened from a repo.

  -h  print this help and exit
  -V  print the version and exit
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "lazyslurm:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("lazyslurm", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	help := fs.Bool("h", false, "")
	showVersion := fs.Bool("V", false, "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	switch {
	case *help:
		fmt.Print(usage)
		return nil
	case *showVersion:
		fmt.Println("lazyslurm", strings.TrimSpace(version))
		return nil
	}
	return ui()
}

func ui() error {
	g, err := gocui.NewGui(gocui.NewGuiOpts{OutputMode: gocui.OutputTrue})
	if err != nil {
		return err
	}
	defer g.Close()
	g.SetManagerFunc(layout)
	if err := bind(g); err != nil {
		return err
	}
	if err := g.MainLoop(); err != nil && !errors.Is(err, gocui.ErrQuit) {
		return err
	}
	return nil
}
