package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
)

type exitError int

func (e exitError) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

func runExec(w io.Writer, root, hostFlag string, args []string) error {
	if len(args) == 0 {
		return usageError("exec needs a command")
	}
	cfg, err := loadConfig(root, hostFlag)
	if err != nil {
		return err
	}
	if cfg.Srun == "" {
		return fmt.Errorf("host %s has no srun key: exec is refused", cfg.Host)
	}
	s, err := syncUp(hookOut, root, cfg, false)
	if err != nil {
		return fmt.Errorf("exec on %s: %w", cfg.Host, err)
	}
	command := args[0]
	if len(args) > 1 {
		command = quoteAll(args)
	}
	line := "cd " + quote(s.Dir+"/work")
	if cfg.Prelude != "" {
		line += " && " + cfg.Prelude
	}
	line += " && export GIT_COMMIT=" + s.Commit + " GIT_DIRTY=0"
	line += " && srun " + cfg.Srun + " bash -c " + quote(command)
	c, _ := cfg.Target.interactive(line, isTerminal(os.Stdin))
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, w, os.Stderr
	if err := c.Run(); err != nil {
		if ee, ok := errors.AsType[*exec.ExitError](err); ok {
			return exitError(max(ee.ExitCode(), 1))
		}
		return fmt.Errorf("exec on %s: %w", cfg.Host, err)
	}
	return nil
}
