package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestJournalRoundTrip(t *testing.T) {
	root := t.TempDir()
	first := Job{Time: "t1", Host: "a", Job: "1", Script: "s.sh", Commit: "abc", Args: []string{"x y"}, Note: "n"}
	second := Job{Time: "t2", Host: "a", Job: "2", Script: "s.sh", Commit: "abc", RetryOf: "1", Array: "3,5"}
	for _, j := range []Job{first, second} {
		if err := appendJob(root, j); err != nil {
			t.Fatal(err)
		}
	}
	got, err := readJournal(root)
	if err != nil {
		t.Fatal(err)
	}
	first.V, second.V = journalVersion, journalVersion
	if !reflect.DeepEqual(got, []Job{first, second}) {
		t.Errorf("got %+v", got)
	}
	data, err := os.ReadFile(journalPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "\n") != 2 {
		t.Errorf("want one line per job:\n%s", data)
	}
	if strings.Contains(strings.Split(string(data), "\n")[1], `"args"`) {
		t.Error("empty args must be omitted")
	}
}

func TestAppendJobCreatesStateDir(t *testing.T) {
	root := t.TempDir()
	if err := appendJob(root, Job{Job: "1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".slurm", "state", "jobs.jsonl")); err != nil {
		t.Errorf("journal: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".slurm", ".gitignore")); err == nil {
		t.Error("no .gitignore must be written")
	}
}

func TestReadJournalOldVersionsAndUnknownFields(t *testing.T) {
	root := t.TempDir()
	write(t, journalPath(root), `{"v":1,"host":"a","job":"1","commit":"11f11e8","dirty":true}

{"v":2,"host":"a","job":"2","future":"x"}
`)
	got, err := readJournal(root)
	if err != nil || len(got) != 2 || got[0].Commit != "11f11e8" || got[1].Job != "2" {
		t.Errorf("got %+v, %v", got, err)
	}
}

func TestReadJournalErrors(t *testing.T) {
	root := t.TempDir()
	write(t, journalPath(root), `{"v":3,"job":"1"}`+"\nnot json\n")
	if _, err := readJournal(root); err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Errorf("bad json: %v", err)
	}
	write(t, journalPath(root), `{"v":4,"job":"1"}`+"\n")
	if _, err := readJournal(root); err == nil || !strings.Contains(err.Error(), "version 4") {
		t.Errorf("future version: %v", err)
	}
}

func TestReadJournalMissingIsEmpty(t *testing.T) {
	got, err := readJournal(t.TempDir())
	if err != nil || len(got) != 0 {
		t.Errorf("got %+v, %v", got, err)
	}
}

func TestOldJournalIsImportedOnce(t *testing.T) {
	root := t.TempDir()
	old := filepath.Join(root, "outputs", "slurms.jsonl")
	write(t, old, `{"v":1,"host":"a","job":"1","commit":"c"}`+"\n")
	if got, err := readJournal(root); err != nil || len(got) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if _, err := os.Stat(old); err != nil {
		t.Error("the old journal must stay in place")
	}
	write(t, old, "")
	if got, err := readJournal(root); err != nil || len(got) != 1 {
		t.Errorf("second read must use the copy: %+v, %v", got, err)
	}
}

func TestSeen(t *testing.T) {
	root := t.TempDir()
	write(t, seenPath(root), "a 1\n2\n\n")
	if err := addSeen(root, "b", "3"); err != nil {
		t.Fatal(err)
	}
	seen, err := readSeen(root)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		job  Job
		want bool
	}{
		{Job{Host: "a", Job: "1"}, true},
		{Job{Host: "b", Job: "1"}, false},
		{Job{Host: "x", Job: "2"}, true},
		{Job{Host: "b", Job: "3"}, true},
		{Job{Host: "a", Job: "3"}, false},
	}
	for _, tt := range tests {
		if got := seen.has(tt.job); got != tt.want {
			t.Errorf("has(%+v) = %v", tt.job, got)
		}
	}
}

func TestOldSeenIsImported(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "outputs", "slurms.seen"), "7\n")
	seen, err := readSeen(root)
	if err != nil || !seen.has(Job{Host: "any", Job: "7"}) {
		t.Errorf("seen = %v, %v", seen, err)
	}
}
