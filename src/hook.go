package main

import (
	"fmt"
	"io"
	"os/exec"
	"strings"
)

func hookEnv(commit string, dirty bool, host string) []string {
	d := "0"
	if dirty {
		d = "1"
	}
	return []string{"GIT_COMMIT=" + commit, "GIT_DIRTY=" + d, "CLUSTER=" + host}
}

func runHook(w io.Writer, name string, c *exec.Cmd) error {
	c.Stdout, c.Stderr = w, w
	if err := c.Run(); err != nil {
		return fmt.Errorf("hook %s: %w", name, err)
	}
	return nil
}

// localHook runs a hook with bash in dir on this machine; an empty script does nothing.
func localHook(w io.Writer, name, dir, script string, env []string) error {
	if script == "" {
		return nil
	}
	c := command("bash", "-c", script)
	c.Dir = dir
	c.Env = append(c.Env, env...)
	return runHook(w, name, c)
}

// targetHook runs a hook in a login shell in dir on the target; an empty script does nothing.
func targetHook(w io.Writer, name string, t Target, dir, script string, env []string) error {
	if script == "" {
		return nil
	}
	parts := make([]string, 0, len(env)+1)
	for _, e := range env {
		parts = append(parts, "export "+quote(e)+";")
	}
	parts = append(parts, "exec bash -lc "+quote("cd "+quote(dir)+" && "+script))
	c, _ := t.cmd(strings.Join(parts, " "))
	return runHook(w, name, c)
}
