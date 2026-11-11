package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const fakeSSH = `#!/bin/sh
while [ "$1" = "-o" ] || [ "$1" = "-t" ]; do
	case "$1" in
	-o) shift 2 ;;
	*) shift ;;
	esac
done
shift
cd "$HOME" && exec sh -c "$*"
`

func setupFakeSSH(t *testing.T) string {
	t.Helper()
	bin, home := t.TempDir(), t.TempDir()
	write(t, filepath.Join(bin, "ssh"), fakeSSH)
	if err := os.Chmod(filepath.Join(bin, "ssh"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("HOME", home)
	return home
}

func testTargets(t *testing.T) map[string]Target {
	t.Helper()
	setupFakeSSH(t)
	return map[string]Target{
		"local": {Dir: filepath.Join(t.TempDir(), "t")},
		"ssh":   {Alias: "fake", Dir: "remote/t"},
	}
}

func TestQuoteSurvivesTheShell(t *testing.T) {
	for _, s := range []string{"", "a b", "it's", `"$HOME" ; rm -rf *`, "tab\there", "new\nline"} {
		out, err := Target{}.run("printf %s "+quote(s), nil)
		if err != nil || string(out) != s {
			t.Errorf("quote(%q) round trip = %q, %v", s, out, err)
		}
	}
}

func TestRunReportsStderrAndStatus(t *testing.T) {
	for name, tg := range testTargets(t) {
		_, err := tg.run("echo oops >&2; exit 3", nil)
		if err == nil || !strings.Contains(err.Error(), "oops") || !strings.Contains(err.Error(), "exit status 3") {
			t.Errorf("%s: err = %v", name, err)
		}
		out, err := tg.run("cat", strings.NewReader("in"))
		if err != nil || string(out) != "in" {
			t.Errorf("%s: stdin = %q, %v", name, out, err)
		}
	}
}

func TestResolve(t *testing.T) {
	home := setupFakeSSH(t)
	abs := filepath.Join(t.TempDir(), "abs")
	tests := []struct {
		name   string
		target Target
		want   string
	}{
		{"local absolute", Target{Dir: abs}, abs},
		{"local tilde", Target{Dir: "~/a b"}, filepath.Join(home, "a b")},
		{"ssh relative", Target{Alias: "fake", Dir: "rel"}, filepath.Join(home, "rel")},
		{"ssh tilde", Target{Alias: "fake", Dir: "~/x"}, filepath.Join(home, "x")},
	}
	for _, tt := range tests {
		got, err := tt.target.resolve()
		if err != nil || got != tt.want {
			t.Errorf("%s: resolve = %q, %v, want %q", tt.name, got, err, tt.want)
		}
		if info, err := os.Stat(tt.want); err != nil || !info.IsDir() {
			t.Errorf("%s: %s was not created", tt.name, tt.want)
		}
	}
}

func TestLockExcludesOthersUntilReleased(t *testing.T) {
	for name, tg := range testTargets(t) {
		dir, err := tg.resolve()
		if err != nil {
			t.Fatal(err)
		}
		unlock, err := tg.lock(dir)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		try := exec.Command("flock", "-n", filepath.Join(dir, ".lock"), "true")
		if try.Run() == nil {
			t.Errorf("%s: lock was not held", name)
		}
		if err := unlock(); err != nil {
			t.Errorf("%s: unlock: %v", name, err)
		}
		if err := exec.Command("flock", "-n", filepath.Join(dir, ".lock"), "true").Run(); err != nil {
			t.Errorf("%s: lock was not released: %v", name, err)
		}
	}
}
