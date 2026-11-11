package main

import (
	"path/filepath"
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
