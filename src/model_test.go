package main

import (
	"regexp"
	"strings"
	"testing"
)

var escapes = regexp.MustCompile("\x1b\\[[0-9;]*m")

func plain(s string) string { return escapes.ReplaceAllString(s, "") }

func testModel() *model {
	return &model{
		hosts: []string{"a", "b"}, host: "a",
		fresh: fresh{
			jobs: []Job{
				{Job: "12", Time: "2026-11-11T10:02:11+01:00", Script: "slurm/train.sh", Commit: "abcdef0123", Note: "big", Host: "a"},
				{Job: "11", Time: "2026-11-10T09:00:00+01:00", Script: "slurm/eval.sh", Host: "a", Array: "2,3", RetryOf: "9"},
			},
			states: map[string]jobState{"12": {States: map[string]int{"RUNNING": 1}}},
			snaps:  []snapshotInfo{{snapSize: snapSize{Commit: strings.Repeat("a", 40), KB: 2048}, Active: 1, Total: 2, State: stateStray, Stray: []string{"results.csv"}}},
		},
		logs: map[string]string{},
	}
}

func TestFilterAndSelectionBounds(t *testing.T) {
	m := testModel()
	m.focus = jobsPanel
	m.move(5)
	if m.sel[jobsPanel] != 1 {
		t.Errorf("sel = %d, want 1", m.sel[jobsPanel])
	}
	m.move(-9)
	if m.sel[jobsPanel] != 0 {
		t.Errorf("sel = %d, want 0", m.sel[jobsPanel])
	}
	m.filter = "EVAL"
	if got := m.visibleJobs(); len(got) != 1 || got[0].Job != "11" {
		t.Errorf("filter eval = %v", got)
	}
	m.filter = "big"
	if got := m.visibleJobs(); len(got) != 1 || got[0].Job != "12" {
		t.Errorf("filter on the note = %v", got)
	}
	m.sel[jobsPanel] = 1
	m.filter = "nothing"
	m.clamp()
	if _, ok := m.job(); ok || m.sel[jobsPanel] != 0 {
		t.Errorf("job selected with no match: sel %d", m.sel[jobsPanel])
	}
}

func TestDetailTexts(t *testing.T) {
	m := testModel()
	m.focus = jobsPanel
	title, text := m.detail()
	text = plain(text)
	for _, want := range []string{"job       12 on a", "slurm/train.sh", "abcdef0123", "note      big", "● 1 RUNNING"} {
		if !strings.Contains(text, want) {
			t.Errorf("job detail lacks %q:\n%s", want, text)
		}
	}
	if title != "Job 12" {
		t.Errorf("title = %q", title)
	}
	m.sel[jobsPanel] = 1
	m.logs["11"] = "tail of the log"
	_, text = m.detail()
	text = plain(text)
	if !strings.Contains(text, "retry of  9") || !strings.HasSuffix(text, "tail of the log") {
		t.Errorf("second job detail:\n%s", text)
	}
	m.focus = snapsPanel
	title, text = m.detail()
	text = plain(text)
	if !strings.Contains(text, "2.0M") || !strings.Contains(text, "stray     results.csv") || title != "Snapshot aaaaaaaa" {
		t.Errorf("snapshot detail %q:\n%s", title, text)
	}
	m.focus = hostsPanel
	if got := m.hostLines(); got[0] != "* a" || got[1] != "  b" {
		t.Errorf("host lines = %q", got)
	}
}

func TestJobLineShowsTheOutcomeAtAGlance(t *testing.T) {
	m := testModel()
	got := m.jobLine(m.jobs[0])
	if plain(got) != "● 12  train  ●1" || !strings.HasPrefix(got, paint(cyan, "●")) {
		t.Errorf("running job line = %q", got)
	}
	m.states["11"] = jobState{States: map[string]int{"COMPLETED": 34, "FAILED": 2}, Final: true}
	got = m.jobLine(m.jobs[1])
	if plain(got) != "✗ 11  eval  ✗2 ✓34" || !strings.HasPrefix(got, paint(red, "✗")) {
		t.Errorf("failed job line = %q", got)
	}
	m.states["11"] = jobState{States: map[string]int{"COMPLETED": 3}, Final: true}
	if got = m.jobLine(m.jobs[1]); !strings.HasPrefix(got, paint(green, "✓")) {
		t.Errorf("completed job line = %q", got)
	}
	if got = (&model{}).jobLine(Job{Job: "5", Script: "a.sh"}); plain(got) != "· 5  a" {
		t.Errorf("unknown job line = %q", got)
	}
}

func TestActiveCountsJobsNotFinal(t *testing.T) {
	m := testModel()
	m.states["11"] = jobState{Final: true}
	if m.active() != 1 {
		t.Errorf("active = %d, want 1", m.active())
	}
}
