package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func quoteDir(dir string) string {
	if rest, ok := strings.CutPrefix(dir, "~/"); ok {
		return `"$HOME"/` + quote(rest)
	}
	return quote(dir)
}

func (t Target) cmd(script string) (*exec.Cmd, string) {
	if t.Alias == "" {
		return command("bash", "-c", script), "bash"
	}
	return command("ssh", "-o", "BatchMode=yes", t.Alias, "bash -c "+quote(script)), "ssh " + t.Alias
}

func (t Target) run(script string, stdin io.Reader) ([]byte, error) {
	c, name := t.cmd(script)
	var out, errOut bytes.Buffer
	c.Stdin, c.Stdout, c.Stderr = stdin, &out, &errOut
	if err := c.Run(); err != nil {
		return nil, fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(errOut.String()))
	}
	return out.Bytes(), nil
}

// resolve creates the target directory if needed and returns its absolute path.
func (t Target) resolve() (string, error) {
	dir := quoteDir(t.Dir)
	out, err := t.run("mkdir -p "+dir+" && cd "+dir+" && pwd", nil)
	if err != nil {
		return "", fmt.Errorf("resolve target %s: %w", t.Dir, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// lock takes flock on dir/.lock and returns the function that releases it.
func (t Target) lock(dir string) (func() error, error) {
	c, name := t.cmd("flock " + quote(dir+"/.lock") + " sh -c 'echo locked; cat'")
	in, err := c.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("lock %s: %w", dir, err)
	}
	out, err := c.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("lock %s: %w", dir, err)
	}
	var errOut bytes.Buffer
	c.Stderr = &errOut
	if err := c.Start(); err != nil {
		return nil, fmt.Errorf("lock %s: %w", dir, err)
	}
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil || line != "locked\n" {
		in.Close()
		c.Wait()
		return nil, fmt.Errorf("lock %s: %s failed: %s", dir, name, strings.TrimSpace(errOut.String()))
	}
	return func() error {
		in.Close()
		return c.Wait()
	}, nil
}
