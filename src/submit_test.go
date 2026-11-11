package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func targetString(tg Target) string {
	if tg.Alias != "" {
		return tg.Alias + ":" + tg.Dir
	}
	return tg.Dir
}

func commitAll(t *testing.T, root string) {
	t.Helper()
	gitRun(t, root, "add", "-A")
	gitRun(t, root, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "x")
}

func jobRepo(t *testing.T, tg Target, extra string) string {
	t.Helper()
	root := t.TempDir()
	gitRun(t, root, "init", "-q")
	write(t, filepath.Join(root, configFile), "[default]\npush = [\"slurm\", \"src\"]\nkeep = [\"outputs\"]\n"+
		"pull = [\"outputs/logs\", \"outputs/missing\"]\nsbatch = \"--partition=p\"\n"+extra+
		"[hosts.h]\ntarget = '"+targetString(tg)+"'\n")
	write(t, filepath.Join(root, "slurm", "run.sh"), "#!/bin/sh\n")
	write(t, filepath.Join(root, "src", "a.py"), "a\n")
	write(t, filepath.Join(root, "other", "x.sh"), "x\n")
	commitAll(t, root)
	return root
}

func submit(root string, args ...string) (string, error) {
	var out bytes.Buffer
	err := runSubmit(&out, root, "", confirmer{Yes: true}, false, "--time=1", "a note", args)
	return out.String(), err
}

func TestSubmitSnapshotsRunsHooksAndJournals(t *testing.T) {
	for name, tg := range testTargets(t) {
		fake := setupFakeSlurm(t)
		root := jobRepo(t, tg, "pre_push = 'echo \"$GIT_COMMIT $GIT_DIRTY\" > pre.txt'\npost_push = 'touch hooked'\n")
		out, err := submit(root, "slurm/run.sh", "x y", "-z")
		if err != nil || out != "job 100 submitted on h\n" {
			t.Fatalf("%s: submit = %q, %v", name, out, err)
		}
		commit, err := headCommit(root)
		if err != nil {
			t.Fatal(err)
		}
		dir, err := tg.resolve()
		if err != nil {
			t.Fatal(err)
		}
		snap := filepath.Join(dir, commit)
		w, werr := os.Stat(filepath.Join(dir, "work", "src", "a.py"))
		s, serr := os.Stat(filepath.Join(snap, "src", "a.py"))
		if werr != nil || serr != nil || !os.SameFile(w, s) {
			t.Errorf("%s: snapshot is not a hardlink: %v %v", name, werr, serr)
		}
		if !exists(filepath.Join(snap, "hooked")) || exists(filepath.Join(dir, "work", "hooked")) {
			t.Errorf("%s: post_push must run in the snapshot only", name)
		}
		if got, _ := os.ReadFile(filepath.Join(root, "pre.txt")); string(got) != commit+" 0\n" {
			t.Errorf("%s: pre_push saw %q", name, got)
		}
		log, _ := os.ReadFile(filepath.Join(fake, "sbatch.log"))
		want := strings.Join([]string{snap, "--parsable", "--export=ALL,GIT_COMMIT=" + commit, "--partition=p",
			"--time=1", "slurm/run.sh", "x y", "-z", "--", ""}, "\n")
		if string(log) != want {
			t.Errorf("%s: sbatch saw %q, want %q", name, log, want)
		}
		jobs, err := readJournal(root)
		if err != nil || len(jobs) != 1 {
			t.Fatalf("%s: journal = %v, %v", name, jobs, err)
		}
		j := jobs[0]
		if j.Host != "h" || j.Job != "100" || j.Script != "slurm/run.sh" || j.Commit != commit ||
			j.Sbatch != "--time=1" || j.Note != "a note" || len(j.Args) != 2 || j.Args[0] != "x y" {
			t.Errorf("%s: journal line = %+v", name, j)
		}
	}
}

func TestSubmitReusesASnapshotAndKeepsOldOnes(t *testing.T) {
	for name, tg := range testTargets(t) {
		setupFakeSlurm(t)
		root := jobRepo(t, tg, "")
		if _, err := submit(root, "slurm/run.sh"); err != nil {
			t.Fatal(err)
		}
		first, err := headCommit(root)
		if err != nil {
			t.Fatal(err)
		}
		dir, err := tg.resolve()
		if err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(dir, first, "stray"), "s\n")
		if out, err := submit(root, "slurm/run.sh"); err != nil || out != "job 101 submitted on h\n" {
			t.Fatalf("%s: second submit = %q, %v", name, out, err)
		}
		if !exists(filepath.Join(dir, first, "stray")) {
			t.Errorf("%s: the snapshot was rebuilt", name)
		}
		write(t, filepath.Join(root, "src", "a.py"), "bb\n")
		commitAll(t, root)
		if _, err := submit(root, "slurm/run.sh"); err != nil {
			t.Fatal(err)
		}
		second, err := headCommit(root)
		if err != nil || second == first {
			t.Fatalf("%s: commits %s %s, %v", name, first, second, err)
		}
		for commit, want := range map[string]string{first: "a\n", second: "bb\n"} {
			if got, _ := os.ReadFile(filepath.Join(dir, commit, "src", "a.py")); string(got) != want {
				t.Errorf("%s: snapshot %s has %q, want %q", name, commit[:8], got, want)
			}
		}
	}
}

func TestSubmitRefusesBadScriptsAndDirtyTrees(t *testing.T) {
	tg := testTargets(t)["local"]
	fake := setupFakeSlurm(t)
	root := jobRepo(t, tg, "")
	write(t, filepath.Join(root, "slurm", "new.sh"), "n\n")
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"untracked", []string{"slurm/new.sh"}, "not tracked"},
		{"outside push", []string{"other/x.sh"}, "push paths"},
		{"absolute", []string{"/etc/passwd"}, "push paths"},
		{"parent", []string{"../x.sh"}, "push paths"},
		{"missing", []string{"slurm/none.sh"}, "not tracked"},
	}
	for _, tt := range tests {
		if _, err := submit(root, tt.args...); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want %q", tt.name, err, tt.want)
		}
	}
	if err := os.Remove(filepath.Join(root, "slurm", "new.sh")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "src", "a.py"), "dirty\n")
	if _, err := submit(root, "slurm/run.sh"); err == nil || !strings.Contains(err.Error(), "not clean") {
		t.Errorf("dirty tree: err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(fake, "sbatch.log")); err == nil {
		t.Error("sbatch ran for a refused submit")
	}
}

func TestSubmitNeedsConfirmation(t *testing.T) {
	tg := testTargets(t)["local"]
	fake := setupFakeSlurm(t)
	root := jobRepo(t, tg, "")
	err := runSubmit(&bytes.Buffer{}, root, "", confirmer{}, false, "", "", []string{"slurm/run.sh"})
	if _, ok := errors.AsType[confirmError](err); !ok {
		t.Fatalf("err = %v", err)
	}
	if exists(filepath.Join(fake, "sbatch.log")) || exists(journalPath(root)) || exists(tg.Dir) && exists(filepath.Join(tg.Dir, "work")) {
		t.Error("an unconfirmed submit changed something")
	}
	err = runSubmit(&bytes.Buffer{}, root, "", confirmer{Yes: true}, false, "", "", nil)
	if _, ok := errors.AsType[usageError](err); !ok {
		t.Errorf("no script: err = %v", err)
	}
}

func TestSubmitStopsWhenAHookFails(t *testing.T) {
	tg := testTargets(t)["local"]
	fake := setupFakeSlurm(t)
	root := jobRepo(t, tg, "post_push = 'exit 5'\n")
	if _, err := submit(root, "slurm/run.sh"); err == nil || !strings.Contains(err.Error(), "post_push") {
		t.Errorf("err = %v", err)
	}
	if exists(filepath.Join(fake, "sbatch.log")) || exists(journalPath(root)) {
		t.Error("a failed hook must stop the submit")
	}
}
