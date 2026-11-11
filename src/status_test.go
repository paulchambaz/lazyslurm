package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureStateDirIsIdempotentAndIgnored(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	for range 2 {
		if err := ensureStateDir(root); err != nil {
			t.Fatal(err)
		}
	}
	write(t, journalPath(root), "{}\n")
	out, err := exec.Command("git", "-C", root, "status", "--porcelain").Output()
	if err != nil || len(out) != 0 {
		t.Errorf("git sees %q, %v", out, err)
	}
}

func statusRepo(t *testing.T) string {
	t.Helper()
	root := writeConfig(t, validConfig)
	write(t, journalPath(root), strings.Join([]string{
		`{"v":3,"time":"t1","host":"pfcalcul","job":"1","script":"a.sh","commit":"0123456789abcdef"}`,
		`{"v":3,"time":"t2","host":"here","job":"2","script":"b.sh","commit":"fedcba9876543210","note":"hi"}`,
		`{"v":3,"time":"t3","host":"pfcalcul","job":"3","script":"c.sh","commit":"abcdef0123456789"}`,
	}, "\n")+"\n")
	write(t, seenPath(root), "pfcalcul 1\n")
	return root
}

func status(t *testing.T, root, host string, all, asJSON bool, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	if err := runStatus(&out, root, host, all, asJSON, args); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestStatusFiltersByHostAndSeen(t *testing.T) {
	t.Setenv("CLUSTER", "")
	root := statusRepo(t)
	if got := status(t, root, "", false, false); got != "3  t3  c.sh  abcdef01\n" {
		t.Errorf("default host = %q", got)
	}
	if got := status(t, root, "", true, false); got != "3  t3  c.sh  abcdef01\n1  t1  a.sh  01234567\n" {
		t.Errorf("--all = %q", got)
	}
	if got := status(t, root, "here", false, false); got != "2  t2  b.sh  fedcba98  hi\n" {
		t.Errorf("-H here = %q", got)
	}
	if got := status(t, root, "", false, false, "1"); got != "1  t1  a.sh  01234567\n" {
		t.Errorf("JOB shows seen jobs too: %q", got)
	}
}

func TestStatusWithoutSelectedHostListsAllHosts(t *testing.T) {
	t.Setenv("CLUSTER", "")
	root := statusRepo(t)
	write(t, filepath.Join(root, configFile), strings.Replace(validConfig, `host    = "pfcalcul"`, "", 1))
	want := "pfcalcul  3  t3  c.sh  abcdef01\nhere  2  t2  b.sh  fedcba98  hi\n"
	if got := status(t, root, "", false, false); got != want {
		t.Errorf("got %q", got)
	}
}

func TestStatusJSON(t *testing.T) {
	t.Setenv("CLUSTER", "")
	root := statusRepo(t)
	want := `{"host":"pfcalcul","job":"3","time":"t3","script":"c.sh","commit":"abcdef0123456789","note":""}` + "\n"
	if got := status(t, root, "", false, true); got != want {
		t.Errorf("got %q", got)
	}
}

func TestStatusUsageAndConfigErrors(t *testing.T) {
	t.Setenv("CLUSTER", "")
	root := statusRepo(t)
	err := runStatus(&bytes.Buffer{}, root, "", false, false, []string{"1", "2"})
	if _, ok := errors.AsType[usageError](err); !ok {
		t.Errorf("two jobs: %v", err)
	}
	if err := runStatus(&bytes.Buffer{}, root, "nope", false, false, nil); err == nil {
		t.Error("unknown host must fail")
	}
	if err := os.Remove(filepath.Join(root, configFile)); err != nil {
		t.Fatal(err)
	}
	if err := runStatus(&bytes.Buffer{}, root, "", false, false, nil); err == nil {
		t.Error("missing config must fail")
	}
}
