package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRefreshQueriesOnlyJobsNotYetFinal(t *testing.T) {
	fake := setupFakeSlurm(t)
	root := jobRepo(t, Target{Dir: filepath.Join(t.TempDir(), "t")}, "")
	for range 2 {
		if _, err := submit(root, "slurm/run.sh"); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := loadConfig(root, "")
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(fake, "sacct"), "100|COMPLETED\n101|RUNNING\n")
	f, err := refresh(root, cfg, fresh{})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.jobs) != 2 || f.jobs[0].Job != "101" || len(f.snaps) != 1 {
		t.Fatalf("jobs %v, snapshots %d", f.jobs, len(f.snaps))
	}
	if !f.states["100"].Final || f.states["101"].Final {
		t.Errorf("states = %v", f.states)
	}
	seen, err := readSeen(root)
	if err != nil || !seen.has(Job{Host: "h", Job: "100"}) || seen.has(Job{Host: "h", Job: "101"}) {
		t.Errorf("seen = %v, %v", seen, err)
	}
	write(t, filepath.Join(fake, "sacct"), "100|FAILED\n101|COMPLETED\n")
	g, err := refresh(root, cfg, f)
	if err != nil {
		t.Fatal(err)
	}
	if g.states["100"].States["COMPLETED"] != 1 || !g.states["101"].Final {
		t.Errorf("final job asked again or running job not: %v", g.states)
	}
}

func TestRefreshKeepsWhatItHadWhenTheClusterFails(t *testing.T) {
	fake := setupFakeSlurm(t)
	root := jobRepo(t, Target{Dir: filepath.Join(t.TempDir(), "t")}, "")
	if _, err := submit(root, "slurm/run.sh"); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(root, "")
	if err != nil {
		t.Fatal(err)
	}
	prev := fresh{states: map[string]jobState{"100": {States: map[string]int{"RUNNING": 1}}}}
	write(t, filepath.Join(fake, "fail"), "")
	f, err := refresh(root, cfg, prev)
	if err == nil {
		t.Fatal("a failing sacct is not an error")
	}
	if len(f.jobs) != 1 || f.states["100"].States["RUNNING"] != 1 {
		t.Errorf("fresh = %v", f)
	}
}

func TestRefreshUsesOneCommandOnTheCluster(t *testing.T) {
	setupFakeSlurm(t)
	root := jobRepo(t, Target{Dir: filepath.Join(t.TempDir(), "t")}, "")
	if _, err := submit(root, "slurm/run.sh"); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(root, "")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	trace = func(line string) {
		if !strings.HasPrefix(line, "git ") {
			calls++
		}
	}
	t.Cleanup(func() { trace = nil })
	if _, err := refresh(root, cfg, fresh{}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("refresh ran %d commands, want 1", calls)
	}
}

func TestFullCommitsExpandsShortHashes(t *testing.T) {
	root := jobRepo(t, Target{Dir: filepath.Join(t.TempDir(), "t")}, "")
	c := command("git", "rev-parse", "HEAD")
	c.Dir = root
	out, err := c.Output()
	if err != nil {
		t.Fatal(err)
	}
	full := strings.TrimSpace(string(out))
	jobs := []Job{{Job: "1", Commit: full[:7]}, {Job: "2", Commit: "deadbee"}, {Job: "3"}}
	got, err := fullCommits(root, jobs, nil)
	if err != nil || got[full[:7]] != full || got["deadbee"] != "" || len(got) != 1 {
		t.Errorf("full commits = %v, %v", got, err)
	}
	again, err := fullCommits(root, jobs, got)
	if err != nil || len(again) != 1 {
		t.Errorf("second call = %v, %v", again, err)
	}
}
