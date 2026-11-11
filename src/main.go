// Command lazyslurm is a terminal UI for Slurm clusters, opened from a repo.
package main

import (
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jesseduffield/gocui"
)

//go:embed VERSION
var version string

const usage = `usage: lazyslurm [-hV] [-H HOST] [--json] [--yes] [COMMAND [ARGS]]

A terminal UI for Slurm clusters, opened from a repo. Without a command it
opens the UI.

commands:
  status [--all] [JOB]  list the jobs of the journal

options:
  -h        print this help and exit
  -V        print the version and exit
  -H HOST   use this host of .slurm.toml, instead of CLUSTER or the default
  --json    print JSON Lines
  --yes     do not ask for confirmation
`

type usageError string

func (e usageError) Error() string { return string(e) }

func main() {
	err := run(os.Args[1:])
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, "lazyslurm:", err)
	if _, ok := errors.AsType[usageError](err); ok {
		os.Exit(2)
	}
	os.Exit(1)
}

func run(args []string) error {
	fs := flag.NewFlagSet("lazyslurm", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	help := fs.Bool("h", false, "")
	showVersion := fs.Bool("V", false, "")
	hostFlag := fs.String("H", "", "")
	asJSON := fs.Bool("json", false, "")
	fs.Bool("yes", false, "")
	all := fs.Bool("all", false, "")
	if err := fs.Parse(args); err != nil {
		return usageError(err.Error())
	}
	command, rest := "", fs.Args()
	if len(rest) > 0 {
		command = rest[0]
		if err := fs.Parse(rest[1:]); err != nil {
			return usageError(err.Error())
		}
		rest = fs.Args()
	}
	switch {
	case *help:
		fmt.Print(usage)
		return nil
	case *showVersion:
		fmt.Println("lazyslurm", strings.TrimSpace(version))
		return nil
	}
	switch command {
	case "":
		return ui()
	case "status":
		root, err := repoRoot()
		if err != nil {
			return err
		}
		return runStatus(os.Stdout, root, *hostFlag, *all, *asJSON, rest)
	}
	return usageError(fmt.Sprintf("unknown command %q", command))
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
