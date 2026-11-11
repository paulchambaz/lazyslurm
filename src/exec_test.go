package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecRunsInWorkThroughSrun(t *testing.T) {
	for name, tg := range testTargets(t) {
		fake := setupFakeSlurm(t)
		root := jobRepo(t, tg, "srun = \"--partition=g --time=1\"\nprelude = \"export P=pre\"\n")
		dir, err := tg.resolve()
		if err != nil {
			t.Fatal(err)
		}
		commit, err := headCommit(root)
		if err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(root, "src", "a.py"), "changed\n")
		var out bytes.Buffer
		if err := runExec(&out, root, "", []string{`echo "$P $GIT_COMMIT $GIT_DIRTY"; pwd; cat src/a.py`}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		want := "pre " + commit + " 1\n" + filepath.Join(dir, "work") + "\nchanged\n"
		if out.String() != want {
			t.Errorf("%s: output = %q, want %q", name, out.String(), want)
		}
		log, _ := os.ReadFile(filepath.Join(fake, "srun.log"))
		wantLog := strings.Join([]string{filepath.Join(dir, "work"), "--partition=g", "--time=1", "bash", "-c",
			`echo "$P $GIT_COMMIT $GIT_DIRTY"; pwd; cat src/a.py`, "--", ""}, "\n")
		if string(log) != wantLog {
			t.Errorf("%s: srun saw %q, want %q", name, log, wantLog)
		}
	}
}

func TestExecQuotesSeveralArgumentsAndReturnsTheStatus(t *testing.T) {
	for name, tg := range testTargets(t) {
		setupFakeSlurm(t)
		root := jobRepo(t, tg, "srun = \"--time=1\"\n")
		var out bytes.Buffer
		if err := runExec(&out, root, "", []string{"printf", "%s|", "a b", "it's"}); err != nil || out.String() != "a b|it's|" {
			t.Errorf("%s: output = %q, %v", name, out.String(), err)
		}
		err := runExec(&out, root, "", []string{"exit 7"})
		if code, ok := err.(exitError); !ok || code != 7 {
			t.Errorf("%s: err = %v, want exit status 7", name, err)
		}
	}
}

func TestExecIsRefusedWithoutSrunOrCommand(t *testing.T) {
	for name, tg := range testTargets(t) {
		setupFakeSlurm(t)
		root := jobRepo(t, tg, "")
		if err := runExec(&bytes.Buffer{}, root, "", []string{"true"}); err == nil || !strings.Contains(err.Error(), "srun") {
			t.Errorf("%s: err = %v", name, err)
		}
		if err := runExec(&bytes.Buffer{}, root, "", nil); err == nil {
			t.Errorf("%s: no command must be a usage error", name)
		}
	}
}
