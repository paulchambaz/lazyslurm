package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func retry(root string, ask confirmer, args ...string) (string, error) {
	var out bytes.Buffer
	err := runRetry(&out, root, "", ask, false, args)
	return out.String(), err
}

func sbatchRecords(t *testing.T, fake string) []string {
	t.Helper()
	log, _ := os.ReadFile(filepath.Join(fake, "sbatch.log"))
	return strings.Split(strings.TrimSuffix(string(log), "--\n"), "--\n")
}

func TestRetryResubmitsFailedTasksOfAnArray(t *testing.T) {
	for name, tg := range testTargets(t) {
		fake := setupFakeSlurm(t)
		root := jobRepo(t, tg, "")
		if _, err := submit(root, "slurm/run.sh", "arg"); err != nil {
			t.Fatal(err)
		}
		commit, _ := headCommit(root)
		write(t, filepath.Join(fake, "sacct"),
			"100_1|COMPLETED\n100_10|FAILED\n100_2|NODE_FAIL\n100_3|RUNNING\n100_[5-9]|PENDING\n")
		out, err := retry(root, confirmer{Yes: true}, "100")
		if err != nil || out != "job 101 submitted on h (retry of 100)\n" {
			t.Fatalf("%s: retry = %q, %v", name, out, err)
		}
		dir, _ := tg.resolve()
		recs := sbatchRecords(t, fake)
		want := strings.Join([]string{filepath.Join(dir, commit), "--parsable", "--export=ALL,GIT_COMMIT=" + commit,
			"--partition=p", "--time=1", "--array=2,10", "slurm/run.sh", "arg", ""}, "\n")
		if len(recs) != 2 || recs[1] != want {
			t.Errorf("%s: sbatch saw %q, want %q", name, recs, want)
		}
		journal, _ := os.ReadFile(filepath.Join(root, ".slurm", "jobs.jsonl"))
		lines := strings.Split(strings.TrimSpace(string(journal)), "\n")
		last := lines[len(lines)-1]
		for _, want := range []string{`"job":"101"`, `"retry_of":"100"`, `"array":"2,10"`, `"sbatch":"--time=1"`, `"args":["arg"]`} {
			if !strings.Contains(last, want) {
				t.Errorf("%s: journal line %s lacks %s", name, last, want)
			}
		}
	}
}

func TestRetryOfAPlainJobAndOfNothing(t *testing.T) {
	for name, tg := range testTargets(t) {
		fake := setupFakeSlurm(t)
		root := jobRepo(t, tg, "")
		if _, err := submit(root, "slurm/run.sh"); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(fake, "sacct"), "100|COMPLETED\n")
		out, err := retry(root, confirmer{})
		if err != nil || out != "nothing to retry for job 100\n" || len(sbatchRecords(t, fake)) != 1 {
			t.Errorf("%s: retry of a completed job = %q, %v", name, out, err)
		}
		write(t, filepath.Join(fake, "sacct"), "100|TIMEOUT\n")
		if _, err := retry(root, confirmer{}); err == nil || !strings.Contains(err.Error(), "--yes") {
			t.Errorf("%s: unconfirmed retry = %v", name, err)
		}
		out, err = retry(root, confirmer{Yes: true})
		if err != nil || out != "job 101 submitted on h (retry of 100)\n" {
			t.Fatalf("%s: retry = %q, %v", name, out, err)
		}
		if recs := sbatchRecords(t, fake); strings.Contains(recs[1], "--array") {
			t.Errorf("%s: a plain job must not get --array: %q", name, recs[1])
		}
		declined := confirmer{Tty: true, In: strings.NewReader("n\n"), Out: &bytes.Buffer{}}
		write(t, filepath.Join(fake, "sacct"), "101|FAILED\n")
		if _, err := retry(root, declined, "101"); err == nil || len(sbatchRecords(t, fake)) != 2 {
			t.Errorf("%s: declined retry = %v", name, err)
		}
	}
}

func TestRetryErrors(t *testing.T) {
	for name, tg := range testTargets(t) {
		fake := setupFakeSlurm(t)
		root := jobRepo(t, tg, "")
		if _, err := retry(root, confirmer{Yes: true}); err == nil || !strings.Contains(err.Error(), "no job") {
			t.Errorf("%s: empty journal = %v", name, err)
		}
		if _, err := retry(root, confirmer{Yes: true}, "9"); err == nil || !strings.Contains(err.Error(), "not in the journal") {
			t.Errorf("%s: unknown job = %v", name, err)
		}
		lost := Job{Time: "t", Host: "h", Job: "55", Script: "slurm/run.sh", Commit: "deadbeefdeadbeef"}
		if err := appendJob(root, lost); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(fake, "sacct"), "55|FAILED\n")
		if _, err := retry(root, confirmer{Yes: true}, "55"); err == nil || !strings.Contains(err.Error(), "not in the local repo") {
			t.Errorf("%s: unknown commit = %v", name, err)
		}
		if _, err := retry(root, confirmer{Yes: true}, "1", "2"); err == nil {
			t.Errorf("%s: two jobs must be a usage error", name)
		}
	}
}

func TestRetryRebuildsALostSnapshotFromTheCommit(t *testing.T) {
	for name, tg := range testTargets(t) {
		fake := setupFakeSlurm(t)
		root := jobRepo(t, tg, "post_push = 'touch hooked'\n")
		if _, err := submit(root, "slurm/run.sh"); err != nil {
			t.Fatal(err)
		}
		commit, _ := headCommit(root)
		write(t, filepath.Join(root, "src", "a.py"), "newer and longer\n")
		commitAll(t, root)
		if err := runPush(&bytes.Buffer{}, root, "", false, nil); err != nil {
			t.Fatal(err)
		}
		dir, _ := tg.resolve()
		if err := os.RemoveAll(filepath.Join(dir, commit)); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(fake, "sacct"), "100|FAILED\n")
		if _, err := retry(root, confirmer{Yes: true}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		snap := filepath.Join(dir, commit)
		if got, _ := os.ReadFile(filepath.Join(snap, "src", "a.py")); string(got) != "a\n" {
			t.Errorf("%s: rebuilt a.py = %q, want the old commit's", name, got)
		}
		if got, _ := os.ReadFile(filepath.Join(dir, "work", "src", "a.py")); string(got) != "newer and longer\n" {
			t.Errorf("%s: work/ was touched: %q", name, got)
		}
		w, werr := os.Stat(filepath.Join(dir, "work", "slurm", "run.sh"))
		s, serr := os.Stat(filepath.Join(snap, "slurm", "run.sh"))
		if werr != nil || serr != nil || !os.SameFile(w, s) {
			t.Errorf("%s: an unchanged file must be a hardlink of work/: %v %v", name, werr, serr)
		}
		if link, err := os.Readlink(filepath.Join(snap, "outputs")); err != nil || link != filepath.Join(dir, "keep", "outputs") {
			t.Errorf("%s: keep symlink = %q, %v", name, link, err)
		}
		if !exists(filepath.Join(snap, "hooked")) || exists(filepath.Join(dir, commit+".tmp")) {
			t.Errorf("%s: post_push must run in the rebuilt snapshot and no .tmp may stay", name)
		}
		if exists(filepath.Join(snap, "other")) {
			t.Errorf("%s: only the push paths belong in a snapshot", name)
		}
	}
}
