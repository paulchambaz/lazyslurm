package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const (
	fakeSbatch = `#!/bin/sh
d="$FAKE_SLURM"
n=$(cat "$d/next" 2>/dev/null || echo 100)
echo $((n + 1)) > "$d/next"
{ pwd; for a in "$@"; do printf '%s\n' "$a"; done; echo --; } >> "$d/sbatch.log"
echo "$n$(cat "$d/suffix" 2>/dev/null)"
`
	fakeScancel = `#!/bin/sh
printf '%s\n' "$@" >> "$FAKE_SLURM/scancel.log"
`
	fakeSrun = `#!/bin/sh
{ pwd; for a in "$@"; do printf '%s\n' "$a"; done; echo --; } >> "$FAKE_SLURM/srun.log"
while [ "${1#-}" != "$1" ]; do shift; done
exec "$@"
`
	fakeQuery = `#!/bin/sh
d="$FAKE_SLURM"
if [ -e "$d/fail" ] || [ -e "$d/fail-$(basename "$0")" ]; then echo boom >&2; exit 1; fi
cat "$d/$(basename "$0")" 2>/dev/null
exit 0
`
)

func setupFakeSlurm(t *testing.T) string {
	t.Helper()
	bin, state := t.TempDir(), t.TempDir()
	for name, body := range map[string]string{
		"sbatch": fakeSbatch, "sacct": fakeQuery, "squeue": fakeQuery, "scontrol": fakeQuery,
		"scancel": fakeScancel, "srun": fakeSrun,
	} {
		write(t, filepath.Join(bin, name), body)
		if err := os.Chmod(filepath.Join(bin, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("FAKE_SLURM", state)
	return state
}

func TestParseSacct(t *testing.T) {
	out := "7|CANCELLED by 1234\n8_1|COMPLETED\n8_2|RUNNING\n8_[5-9]|PENDING\n\nbroken\n9|\n"
	want := map[string][]string{
		"7": {"CANCELLED"},
		"8": {"COMPLETED", "RUNNING", "PENDING"},
	}
	if got := parseSacct(out); !reflect.DeepEqual(got, want) {
		t.Errorf("parseSacct = %v, want %v", got, want)
	}
}

func TestParseSqueue(t *testing.T) {
	want := map[string][]string{"7": {"/a"}, "8": {"/b", "/c"}}
	if got := parseSqueue("7|/a\n8_1|/b\n8_2|/c\n\n"); !reflect.DeepEqual(got, want) {
		t.Errorf("parseSqueue = %v, want %v", got, want)
	}
}

func TestJudge(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	old := now.Add(-2 * time.Hour).Format(time.RFC3339)
	young := now.Add(-time.Minute).Format(time.RFC3339)
	tests := []struct {
		name   string
		job    Job
		rows   map[string][]string
		queued map[string][]string
		final  bool
	}{
		{"all final", Job{Job: "1", Time: young}, map[string][]string{"1": {"COMPLETED", "FAILED"}}, nil, true},
		{"one running", Job{Job: "1", Time: young}, map[string][]string{"1": {"COMPLETED", "RUNNING"}}, nil, false},
		{"unknown state is active", Job{Job: "1", Time: old}, map[string][]string{"1": {"WEIRD"}}, nil, false},
		{"no rows, young", Job{Job: "1", Time: young}, nil, nil, false},
		{"no rows, old", Job{Job: "1", Time: old}, nil, nil, true},
		{"no rows, old, queued", Job{Job: "1", Time: old}, nil, map[string][]string{"1": {"/x"}}, false},
		{"no rows, unparsable time", Job{Job: "1", Time: "t1"}, nil, nil, false},
	}
	for _, tt := range tests {
		got := judge([]Job{tt.job}, tt.rows, tt.queued, now)["1"]
		if got.Final != tt.final {
			t.Errorf("%s: final = %v, want %v", tt.name, got.Final, tt.final)
		}
	}
}

func TestFetchStatesAndItsFailure(t *testing.T) {
	for name, tg := range testTargets(t) {
		fake := setupFakeSlurm(t)
		write(t, filepath.Join(fake, "sacct"), "1_1|COMPLETED\n1_2|RUNNING\n2|FAILED\n")
		write(t, filepath.Join(fake, "squeue"), "1_2|/snap\n")
		jobs := []Job{{Job: "1", Time: "t"}, {Job: "2", Time: "t"}}
		got, _, err := fetchStates(tg, jobs, time.Now())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if s := got["1"]; s.Final || s.States["COMPLETED"] != 1 || s.States["RUNNING"] != 1 {
			t.Errorf("%s: job 1 = %+v", name, s)
		}
		if s := got["2"]; !s.Final || s.States["FAILED"] != 1 {
			t.Errorf("%s: job 2 = %+v", name, s)
		}
		write(t, filepath.Join(fake, "fail"), "")
		if _, _, err := fetchStates(tg, jobs, time.Now()); err == nil || !strings.Contains(err.Error(), "boom") {
			t.Errorf("%s: failing sacct = %v", name, err)
		}
	}
}

func TestSbatchBuildsTheCommandAndReadsTheId(t *testing.T) {
	for name, tg := range testTargets(t) {
		fake := setupFakeSlurm(t)
		write(t, filepath.Join(fake, "suffix"), ";cluster")
		dir, err := tg.resolve()
		if err != nil {
			t.Fatal(err)
		}
		cfg := Config{Target: tg, Sbatch: "--partition=p"}
		id, err := sbatch(cfg, dir, "abc", "s h.sh", []string{"a b", "it's"}, "--time=1 --array=1-2")
		if err != nil || id != "100" {
			t.Fatalf("%s: sbatch = %q, %v", name, id, err)
		}
		log, _ := os.ReadFile(filepath.Join(fake, "sbatch.log"))
		want := strings.Join([]string{dir, "--parsable", "--export=ALL,GIT_COMMIT=abc", "--partition=p",
			"--time=1", "--array=1-2", "s h.sh", "a b", "it's", "--", ""}, "\n")
		if string(log) != want {
			t.Errorf("%s: sbatch saw %q, want %q", name, log, want)
		}
	}
}

func TestParseTasksKeepsTheTaskIds(t *testing.T) {
	want := []taskRow{{"7_1", "CANCELLED"}, {"7_[4-9]", "PENDING"}}
	if got := parseTasks("7_1|CANCELLED by 1234\n7_[4-9]|PENDING\nbroken\n8|\n"); !reflect.DeepEqual(got, want) {
		t.Errorf("parseTasks = %v, want %v", got, want)
	}
}

func TestFailedTasks(t *testing.T) {
	rows := []taskRow{{"7_1", "COMPLETED"}, {"7_10", "FAILED"}, {"7_2", "TIMEOUT"}, {"7_3", "RUNNING"}, {"7_[5-9]", "PENDING"}}
	if array, failed := failedTasks(rows); array != "2,10" || !failed {
		t.Errorf("failedTasks = %q, %v", array, failed)
	}
	if array, failed := failedTasks([]taskRow{{"8", "FAILED"}}); array != "" || !failed {
		t.Errorf("plain job = %q, %v", array, failed)
	}
	if _, failed := failedTasks([]taskRow{{"8", "COMPLETED"}, {"9", "RUNNING"}}); failed {
		t.Error("completed and running tasks are not failures")
	}
}

func TestQueueQueriesAndTheirParsing(t *testing.T) {
	for name, tg := range testTargets(t) {
		fake := setupFakeSlurm(t)
		write(t, filepath.Join(fake, "sacct"), "7_1|COMPLETED\n7_2|FAILED\n")
		write(t, filepath.Join(fake, "squeue"), "7_3|/a\n8|/b\n\n")
		write(t, filepath.Join(fake, "scontrol"), "JobId=7\n   StdErr=/e.txt\n   StdOut=/o.txt\n")
		rows, err := taskRows(tg, "7")
		if err != nil || len(rows) != 2 || rows[1] != (taskRow{"7_2", "FAILED"}) {
			t.Errorf("%s: taskRows = %v, %v", name, rows, err)
		}
		ids, err := activeIDs(tg)
		if err != nil || !reflect.DeepEqual(ids, []string{"7_3", "8"}) {
			t.Errorf("%s: activeIDs = %v, %v", name, ids, err)
		}
		out, errPath, err := jobLogPaths(tg, "7")
		if err != nil || out != "/o.txt" || errPath != "/e.txt" {
			t.Errorf("%s: jobLogPaths = %q %q, %v", name, out, errPath, err)
		}
		_, dirs, err := fetchStates(tg, nil, time.Now())
		if err != nil || len(dirs) != 2 {
			t.Errorf("%s: queue dirs = %v, %v", name, dirs, err)
		}
		write(t, filepath.Join(fake, "fail-scontrol"), "")
		if _, _, err := jobLogPaths(tg, "7"); err == nil {
			t.Errorf("%s: a failing scontrol must be an error", name)
		}
	}
}

func TestQueuedMatchesTasksAndRanges(t *testing.T) {
	ids := []string{"7_2", "7_[4-6,9%2]", "8"}
	for id, want := range map[string]bool{"7_2": true, "7_5": true, "7_9": true, "7_3": false, "7_7": false, "7": true, "8": true, "8_1": false, "9": false} {
		if got := queued(ids, id); got != want {
			t.Errorf("queued(%s) = %v, want %v", id, got, want)
		}
	}
}
