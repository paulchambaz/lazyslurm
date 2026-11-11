package tests

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "lazyslurm")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	binary = filepath.Join(dir, "lazyslurm")
	if out, err := exec.Command("go", "build", "-o", binary, "../src").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build: %v\n%s", err, out)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func lazyslurm(t *testing.T, dir string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CLUSTER=")
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		return out.String(), errOut.String(), exit.ExitCode()
	}
	if err != nil {
		t.Fatal(err)
	}
	return out.String(), errOut.String(), 0
}

func repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	return dir
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestVersionPrintsTheVersionFile(t *testing.T) {
	want, err := os.ReadFile("../src/VERSION")
	if err != nil {
		t.Fatal(err)
	}
	out, _, code := lazyslurm(t, ".", "-V")
	if code != 0 || strings.TrimSpace(out) != "lazyslurm "+strings.TrimSpace(string(want)) {
		t.Errorf("-V = %q, exit %d", out, code)
	}
}

func TestHelpPrintsUsage(t *testing.T) {
	out, _, code := lazyslurm(t, ".", "-h")
	if code != 0 || !strings.HasPrefix(out, "usage: lazyslurm") {
		t.Errorf("-h = %q, exit %d", out, code)
	}
}

func TestUnknownFlagExitsWithUsageCode(t *testing.T) {
	if _, _, code := lazyslurm(t, ".", "-x"); code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
}

func TestUnknownCommandExitsWithUsageCode(t *testing.T) {
	_, stderr, code := lazyslurm(t, ".", "frobnicate")
	if code != 2 || !strings.Contains(stderr, "frobnicate") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
}

func TestStatusListsTheJournal(t *testing.T) {
	dir := repo(t)
	write(t, filepath.Join(dir, ".slurm.toml"), `
[default]
push = ["src"]
[hosts.a]
target = "/scratch/x"
`)
	write(t, filepath.Join(dir, ".slurm", "jobs.jsonl"),
		`{"v":3,"time":"t1","host":"a","job":"1","script":"s.sh","commit":"0123456789abcdef"}`+"\n"+
			`{"v":3,"time":"t2","host":"a","job":"2","script":"s.sh","commit":"0123456789abcdef"}`+"\n")
	write(t, filepath.Join(dir, ".slurm", "seen"), "a 1\n")

	out, _, code := lazyslurm(t, dir, "status")
	if code != 0 || out != "2  t2  s.sh  01234567\n" {
		t.Errorf("status = %q, exit %d", out, code)
	}
	out, _, _ = lazyslurm(t, dir, "status", "--all", "--json")
	if strings.Count(out, "\n") != 2 || !strings.HasPrefix(out, `{"host":"a","job":"2"`) {
		t.Errorf("status --all --json = %q", out)
	}
	_, stderr, code := lazyslurm(t, dir, "-H", "nope", "status")
	if code != 1 || !strings.Contains(stderr, "a") {
		t.Errorf("-H nope: exit %d, stderr %q", code, stderr)
	}
}

func TestStatusOutsideARepoFails(t *testing.T) {
	_, stderr, code := lazyslurm(t, t.TempDir(), "status")
	if code != 1 || !strings.Contains(stderr, "not a git repository") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
}

func TestStatusReportsABrokenConfig(t *testing.T) {
	dir := repo(t)
	write(t, filepath.Join(dir, ".slurm.toml"), "[default]\nbogus = 1\n")
	_, stderr, code := lazyslurm(t, dir, "status")
	if code != 1 || !strings.Contains(stderr, "bogus") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
}
