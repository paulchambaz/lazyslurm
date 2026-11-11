package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

type confirmError string

func (e confirmError) Error() string { return string(e) }

// confirmer asks before a command changes the cluster: --yes, a prompt on a terminal, else exit 3.
type confirmer struct {
	Yes bool
	Tty bool
	In  io.Reader
	Out io.Writer
}

func (c confirmer) ask(what string) error {
	if c.Yes {
		return nil
	}
	if !c.Tty {
		return confirmError(what + ": nothing done, pass --yes to confirm")
	}
	if _, err := fmt.Fprintf(c.Out, "%s [y/N] ", what); err != nil {
		return err
	}
	line, err := bufio.NewReader(c.In).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read answer: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return nil
	}
	return errors.New("declined")
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	null, err := os.Stat(os.DevNull)
	return err != nil || !os.SameFile(info, null)
}
