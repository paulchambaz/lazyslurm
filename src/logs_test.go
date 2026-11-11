package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func logs(root string, stderr bool, id string) (string, error) {
	var out bytes.Buffer
	err := runLogs(&out, root, "", false, stderr, []string{id})
	return out.String(), err
}

func TestLogsTriesSlurmThenTheSnapshotThenTheLocalCopy(t *testing.T) {
	for name, tg := range testTargets(t) {
		fake := setupFakeSlurm(t)
		root := jobRepo(t, tg, "")
		if _, err := submit(root, "slurm/run.sh"); err != nil {
			t.Fatal(err)
		}
		commit, _ := headCommit(root)
		dir, _ := tg.resolve()
		spool := t.TempDir()
		write(t, filepath.Join(spool, "o.txt"), "from slurm\n")
		write(t, filepath.Join(spool, "e.txt"), "err from slurm\n")
		write(t, filepath.Join(fake, "scontrol"), "JobId=100\n   StdErr="+filepath.Join(spool, "e.txt")+"\n   StdOut="+filepath.Join(spool, "o.txt")+"\n")
		write(t, filepath.Join(dir, commit, "slurm-100.out"), "from snapshot\n")
		write(t, filepath.Join(dir, commit, "slurm-100.err"), "err from snapshot\n")
		write(t, filepath.Join(dir, commit, "slurm-100_3.out"), "task\n")
		write(t, filepath.Join(root, ".slurm", "logs", commit, "slurm-100.out"), "from local\n")
		for _, step := range []struct {
			stderr bool
			id     string
			want   string
		}{{false, "100", "from slurm\n"}, {true, "100", "err from slurm\n"}} {
			if out, err := logs(root, step.stderr, step.id); err != nil || out != step.want {
				t.Errorf("%s: logs = %q, %v, want %q", name, out, err, step.want)
			}
		}
		write(t, filepath.Join(fake, "fail-scontrol"), "")
		for _, step := range []struct {
			stderr bool
			id     string
			want   string
		}{{false, "100", "from snapshot\n"}, {true, "100", "err from snapshot\n"}, {false, "100_3", "task\n"}} {
			if out, err := logs(root, step.stderr, step.id); err != nil || out != step.want {
				t.Errorf("%s: logs from the snapshot = %q, %v, want %q", name, out, err, step.want)
			}
		}
		if err := os.RemoveAll(filepath.Join(dir, commit)); err != nil {
			t.Fatal(err)
		}
		if out, err := logs(root, false, "100"); err != nil || out != "from local\n" {
			t.Errorf("%s: logs from the local copy = %q, %v", name, out, err)
		}
		if _, err := logs(root, true, "100"); err == nil || !strings.Contains(err.Error(), "no log found") {
			t.Errorf("%s: logs with nothing anywhere = %v", name, err)
		}
		if _, err := logs(root, false, "999"); err == nil || !strings.Contains(err.Error(), "no log found") {
			t.Errorf("%s: logs of an unknown job = %v", name, err)
		}
		if err := runLogs(&bytes.Buffer{}, root, "", false, false, nil); err == nil {
			t.Errorf("%s: no job must be a usage error", name)
		}
	}
}
