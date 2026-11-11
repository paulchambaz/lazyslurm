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
	fakeQuery = `#!/bin/sh
d="$FAKE_SLURM"
if [ -e "$d/fail" ]; then echo boom >&2; exit 1; fi
cat "$d/$(basename "$0")" 2>/dev/null
exit 0
`
)

func setupFakeSlurm(t *testing.T) string {
	t.Helper()
	bin, state := t.TempDir(), t.TempDir()
	for name, body := range map[string]string{"sbatch": fakeSbatch, "sacct": fakeQuery, "squeue": fakeQuery} {
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
		got, err := fetchStates(tg, jobs, time.Now())
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
		if _, err := fetchStates(tg, jobs, time.Now()); err == nil || !strings.Contains(err.Error(), "boom") {
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
