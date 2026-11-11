package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// hookOut receives the output of hooks; the UI points it at its log panel.
var hookOut io.Writer = os.Stderr

// trace, when set, is told the command line of every command before it starts.
var trace func(string)

// command returns an exec.Cmd without LD_LIBRARY_PATH, so system tools work in a nix shell.
func command(name string, args ...string) *exec.Cmd {
	c := exec.Command(name, args...)
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "LD_LIBRARY_PATH=") {
			c.Env = append(c.Env, e)
		}
	}
	if trace != nil {
		trace(strings.Join(c.Args, " "))
	}
	return c
}

func repoRoot() (string, error) {
	out, err := command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", errors.New("not a git repository")
	}
	return strings.TrimSpace(string(out)), nil
}

// stateDir is where lazyslurm keeps what is local to this machine and not worth tracking.
func stateDir(root string) string {
	return filepath.Join(root, ".slurm", "state")
}

func ensureStateDir(root string) error {
	dir := stateDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	return nil
}
