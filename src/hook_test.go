package main

import (
	"bytes"
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

func TestLocalHookRunsInDirWithEnv(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	if err := localHook(&out, "pre_push", dir, `pwd; echo "$GIT_COMMIT $GIT_DIRTY"; echo err >&2`, hookEnv("abc", true, "h")); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, dir+"\nabc 1\n") || !strings.Contains(got, "err") {
		t.Errorf("output = %q", got)
	}
}

func TestEmptyHooksDoNothing(t *testing.T) {
	var out bytes.Buffer
	for name, tg := range testTargets(t) {
		if err := localHook(&out, "h", t.TempDir(), "", nil); err != nil {
			t.Errorf("%s: local: %v", name, err)
		}
		if err := targetHook(&out, "h", tg, "/nonexistent", "", nil); err != nil {
			t.Errorf("%s: target: %v", name, err)
		}
	}
	if out.Len() != 0 {
		t.Errorf("output = %q", out.String())
	}
}

func TestTargetHookRunsInDirWithEnv(t *testing.T) {
	for name, tg := range testTargets(t) {
		dir, err := tg.resolve()
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := targetHook(&out, "post_push", tg, dir, `pwd; echo "$GIT_COMMIT $GIT_DIRTY"`, hookEnv("abc", false, "h")); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := out.String(); !strings.Contains(got, dir+"\nabc 0\n") {
			t.Errorf("%s: output = %q", name, got)
		}
	}
}

func TestFailingHookReportsItsStatus(t *testing.T) {
	var out bytes.Buffer
	err := localHook(&out, "post_pull", t.TempDir(), "echo bad; exit 7", nil)
	exit, ok := errors.AsType[*exec.ExitError](err)
	if !ok || exit.ExitCode() != 7 || !strings.Contains(err.Error(), "post_pull") || !strings.Contains(out.String(), "bad") {
		t.Errorf("err = %v, output = %q", err, out.String())
	}
	for name, tg := range testTargets(t) {
		out.Reset()
		err := targetHook(&out, "pre_pull", tg, t.TempDir(), "echo worse; exit 4", nil)
		if err == nil || !strings.Contains(err.Error(), "pre_pull") || !strings.Contains(out.String(), "worse") {
			t.Errorf("%s: err = %v, output = %q", name, err, out.String())
		}
	}
}

func TestHooksSeeTheWidthOfTheLogPanelOnlyWhenTheUIRunsThem(t *testing.T) {
	t.Cleanup(func() { hookColumns = 0 })
	hasColumns := func() bool {
		return slices.ContainsFunc(hookEnv("abc", false, "h"), func(e string) bool { return strings.HasPrefix(e, "COLUMNS=") })
	}
	if hasColumns() {
		t.Error("COLUMNS is set outside the UI")
	}
	hookColumns = 57
	var out bytes.Buffer
	if err := localHook(&out, "post_pull", t.TempDir(), `echo "$COLUMNS"`, hookEnv("abc", false, "h")); err != nil || out.String() != "57\n" {
		t.Errorf("COLUMNS = %q, %v", out.String(), err)
	}
}
