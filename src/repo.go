package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// command returns an exec.Cmd without LD_LIBRARY_PATH, so system tools work in a nix shell.
func command(name string, args ...string) *exec.Cmd {
	c := exec.Command(name, args...)
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "LD_LIBRARY_PATH=") {
			c.Env = append(c.Env, e)
		}
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

func stateDir(root string) string {
	return filepath.Join(root, ".slurm")
}

func ensureStateDir(root string) error {
	dir := stateDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	ignore := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(ignore, []byte("*\n"), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", ignore, err)
	}
	return nil
}
