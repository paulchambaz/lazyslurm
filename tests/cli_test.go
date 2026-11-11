package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func lazyslurm(t *testing.T, args ...string) (string, error) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "lazyslurm")
	build := exec.Command("go", "build", "-o", bin, "../src")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	out, err := exec.Command(bin, args...).Output()
	return string(out), err
}

func TestVersionPrintsTheVersionFile(t *testing.T) {
	want, err := os.ReadFile("../src/VERSION")
	if err != nil {
		t.Fatal(err)
	}
	out, err := lazyslurm(t, "-V")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out); got != "lazyslurm "+strings.TrimSpace(string(want)) {
		t.Errorf("-V = %q", got)
	}
}

func TestHelpPrintsUsage(t *testing.T) {
	out, err := lazyslurm(t, "-h")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "usage: lazyslurm") {
		t.Errorf("-h = %q", out)
	}
}

func TestUnknownFlagFails(t *testing.T) {
	if _, err := lazyslurm(t, "-x"); err == nil {
		t.Error("expected a non-zero exit")
	}
}
