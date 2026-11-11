package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

const (
	askpassCommand = "__askpass"
	askpassEnv     = "LAZYSLURM_ASKPASS"
	askpassLimit   = 1 << 16
)

var credentialPrompts = []struct {
	pattern *regexp.Regexp
	title   string
	secret  bool
}{
	{regexp.MustCompile(`Enter\s*passphrase\s*for\s*key\s*'.+':`), "Passphrase", true},
	{regexp.MustCompile(`Enter\s*PIN\s*for\s*.+\s*key\s*.+:`), "PIN", true},
	{regexp.MustCompile(`2FA Token`), "Token", true},
	{regexp.MustCompile(`Username\s*for\s*'.+':`), "Username", false},
	{regexp.MustCompile(`Password\s*for\s*'.+':`), "Password", true},
	{regexp.MustCompile(`.+'s password:`), "Password", true},
	{regexp.MustCompile(`Password:`), "Password", true},
}

// credentialTitle names what an ssh prompt asks for, like lazygit does, and says whether to hide the answer.
func credentialTitle(prompt string) (title string, secret bool) {
	for _, c := range credentialPrompts {
		if c.pattern.MatchString(prompt) {
			return c.title, c.secret
		}
	}
	lines := strings.Split(strings.TrimSpace(prompt), "\n")
	return strings.TrimSpace(lines[len(lines)-1]), false
}

// askpass answers the passwords that ssh asks for through SSH_ASKPASS, with the user's input.
type askpass struct {
	dir  string
	ln   net.Listener
	stop chan struct{}
	wg   sync.WaitGroup
}

// startAskpass listens for prompts; ask runs once per prompt and gives up when stop is closed.
func startAskpass(ask func(prompt string, stop <-chan struct{}) (string, bool)) (*askpass, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("askpass: %w", err)
	}
	dir, err := os.MkdirTemp("", "lazyslurm-askpass-")
	if err != nil {
		return nil, fmt.Errorf("askpass: %w", err)
	}
	script := "#!/bin/sh\nexec " + quote(exe) + " " + askpassCommand + " \"$1\"\n"
	if err := os.WriteFile(filepath.Join(dir, "askpass"), []byte(script), 0o700); err != nil {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("askpass: %w", err)
	}
	ln, err := net.Listen("unix", filepath.Join(dir, "sock"))
	if err != nil {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("askpass: %w", err)
	}
	a := &askpass{dir: dir, ln: ln, stop: make(chan struct{})}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			a.wg.Add(1)
			go func() {
				defer a.wg.Done()
				defer conn.Close()
				prompt, err := io.ReadAll(io.LimitReader(conn, askpassLimit))
				if err != nil {
					return
				}
				if answer, ok := ask(string(prompt), a.stop); ok {
					io.WriteString(conn, "+"+answer)
				}
			}()
		}
	}()
	return a, nil
}

// env lists the variables that make ssh ask us, whatever its terminal.
func (a *askpass) env() []string {
	return []string{
		"SSH_ASKPASS=" + filepath.Join(a.dir, "askpass"),
		"SSH_ASKPASS_REQUIRE=force",
		askpassEnv + "=" + filepath.Join(a.dir, "sock"),
	}
}

// close stops listening, releases the prompts still waiting and removes the socket.
func (a *askpass) close() {
	close(a.stop)
	a.ln.Close()
	a.wg.Wait()
	os.RemoveAll(a.dir)
}

// runAskpass is what ssh runs for SSH_ASKPASS: it relays the prompt to the UI and prints the answer.
func runAskpass(args []string) int {
	sock := os.Getenv(askpassEnv)
	if sock == "" {
		fmt.Fprintln(os.Stderr, "lazyslurm: no UI to ask for a password")
		return 1
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lazyslurm:", err)
		return 1
	}
	defer conn.Close()
	if _, err := io.WriteString(conn, strings.Join(args, " ")); err != nil {
		return 1
	}
	if unix, ok := conn.(*net.UnixConn); ok {
		unix.CloseWrite()
	}
	answer, err := io.ReadAll(io.LimitReader(conn, askpassLimit))
	if err != nil || len(answer) == 0 {
		return 1
	}
	fmt.Println(string(answer[1:]))
	return 0
}
