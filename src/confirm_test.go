package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestConfirmer(t *testing.T) {
	tests := []struct {
		name string
		c    confirmer
		in   string
		ok   bool
		kind string
	}{
		{"yes flag", confirmer{Yes: true}, "", true, ""},
		{"y", confirmer{Tty: true}, "y\n", true, ""},
		{"yes word", confirmer{Tty: true}, " YES \n", true, ""},
		{"n", confirmer{Tty: true}, "n\n", false, "declined"},
		{"empty line", confirmer{Tty: true}, "\n", false, "declined"},
		{"eof", confirmer{Tty: true}, "", false, "declined"},
		{"no terminal", confirmer{}, "y\n", false, "--yes"},
	}
	for _, tt := range tests {
		var out bytes.Buffer
		tt.c.In, tt.c.Out = strings.NewReader(tt.in), &out
		err := tt.c.ask("would do it")
		if (err == nil) != tt.ok || (err != nil && !strings.Contains(err.Error(), tt.kind)) {
			t.Errorf("%s: err = %v", tt.name, err)
		}
		_, isConfirm := errors.AsType[confirmError](err)
		if isConfirm != (tt.kind == "--yes") {
			t.Errorf("%s: confirmError = %v", tt.name, isConfirm)
		}
		if asked := strings.Contains(out.String(), "would do it [y/N] "); asked != tt.c.Tty {
			t.Errorf("%s: prompt printed = %v", tt.name, asked)
		}
	}
}
