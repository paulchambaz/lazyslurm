// Command lazyslurm is a terminal UI for Slurm clusters, opened from a repo.
package main

import (
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
)

//go:embed VERSION
var version string

var commands = []string{
	"cancel", "drop", "exec", "logs", "pull", "push", "retry", "snapshots", "status", "submit",
}

const usage = `usage: lazyslurm [-hV] [-H HOST] [--json] [--yes] [COMMAND [ARGS]]

A terminal UI for Slurm clusters, opened from a repo. Without a command it
opens the UI.

commands:
  cancel JOB[_TASK]     cancel an active job
  drop COMMIT           delete a snapshot, by hash prefix
  exec [--] COMMAND...  run COMMAND in work/ on a short allocation
  logs [-f] [--err] JOB[_TASK]
                        print the stdout of a job, or its stderr
  pull                  fetch the pull paths and the snapshot logs, then
                        delete the snapshots that are done
  push                  send the push set to work/ on the target
  retry [JOB]           resubmit the failed tasks of a job
  snapshots             list the snapshots, their size and their state
  status [--all] [JOB]  list the jobs of the journal with their states
  submit [--sbatch OPTS] [--note TEXT] SCRIPT [ARGS...]
                        snapshot HEAD and submit SCRIPT with sbatch

options:
  -h        print this help and exit
  -V        print the version and exit
  -H HOST   use this host of .slurm/config, instead of CLUSTER or the default
  --json    print JSON Lines
  --yes     do not ask for confirmation (without a terminal, the commands
            that change the cluster exit 3 instead)
`

type usageError string

func (e usageError) Error() string { return string(e) }

func main() {
	err := run(os.Args[1:])
	if err == nil {
		return
	}
	if code, ok := errors.AsType[exitError](err); ok {
		os.Exit(int(code))
	}
	fmt.Fprintln(os.Stderr, "lazyslurm:", err)
	if _, ok := errors.AsType[usageError](err); ok {
		os.Exit(2)
	}
	if _, ok := errors.AsType[confirmError](err); ok {
		os.Exit(3)
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
	yes := fs.Bool("yes", false, "")
	sbatchFlag := fs.String("sbatch", "", "")
	note := fs.String("note", "", "")
	all := fs.Bool("all", false, "")
	follow := fs.Bool("f", false, "")
	stderr := fs.Bool("err", false, "")
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
	if command != "" && !slices.Contains(commands, command) {
		return usageError(fmt.Sprintf("unknown command %q", command))
	}
	root, err := repoRoot()
	if err != nil {
		return err
	}
	if command == "" {
		return runTUI(root, *hostFlag)
	}
	ask := confirmer{Yes: *yes, Tty: isTerminal(os.Stdin), In: os.Stdin, Out: os.Stderr}
	switch command {
	case "cancel":
		return runCancel(os.Stdout, root, *hostFlag, ask, *asJSON, rest)
	case "drop":
		return runDrop(os.Stdout, root, *hostFlag, ask, *asJSON, rest)
	case "exec":
		return runExec(os.Stdout, root, *hostFlag, rest)
	case "logs":
		return runLogs(os.Stdout, root, *hostFlag, *follow, *stderr, rest)
	case "pull":
		return runPull(os.Stdout, root, *hostFlag, *asJSON, rest)
	case "push":
		return runPush(os.Stdout, root, *hostFlag, *asJSON, rest)
	case "retry":
		return runRetry(os.Stdout, root, *hostFlag, ask, *asJSON, rest)
	case "snapshots":
		return runSnapshots(os.Stdout, root, *hostFlag, *asJSON, rest)
	case "status":
		return runStatus(os.Stdout, root, *hostFlag, *all, *asJSON, rest)
	}
	return runSubmit(os.Stdout, root, *hostFlag, ask, *asJSON, *sbatchFlag, *note, rest)
}
