package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func rep(c string) string { return strings.Repeat(c, 40) }

func TestClassify(t *testing.T) {
	a, b, c, d, e, f := rep("a"), rep("b"), rep("c"), rep("d"), rep("e"), rep("f")
	snaps := []snapSize{{a, 1}, {b, 2}, {c, 3}, {d, 4}, {e, 5}, {f, 6}}
	jobs := []Job{
		{Job: "1", Commit: a}, {Job: "2", Commit: b}, {Job: "3", Commit: b},
		{Job: "4", Commit: c}, {Job: "5", Commit: e[:7]},
	}
	states := map[string]jobState{
		"1": {Final: true}, "2": {Final: true}, "3": {}, "4": {Final: true}, "5": {},
	}
	queue := []string{"/t/" + d + "/sub", "/t/" + f + "x"}
	stray := map[string][]string{c: {"x.csv"}}
	got := classify(snaps, jobs, states, queue, "/t", stray)
	want := []struct {
		active, total int
		state         snapState
	}{
		{0, 1, stateFinal}, {1, 2, stateActive}, {0, 1, stateStray},
		{0, 0, stateActive}, {1, 1, stateActive}, {0, 0, stateFinal},
	}
	for i, w := range want {
		if got[i].Active != w.active || got[i].Total != w.total || got[i].State != w.state {
			t.Errorf("snapshot %d = %d/%d %s, want %d/%d %s", i, got[i].Active, got[i].Total, got[i].State,
				w.active, w.total, w.state)
		}
	}
}

func submitTwice(t *testing.T, tg Target, extra string) (root, fake, dir string, commits [2]string) {
	t.Helper()
	fake = setupFakeSlurm(t)
	root = jobRepo(t, tg, extra)
	dir, err := tg.resolve()
	if err != nil {
		t.Fatal(err)
	}
	for i, content := range []string{strings.Repeat("x", 200000), "b\n"} {
		write(t, filepath.Join(root, "src", "a.py"), content)
		commitAll(t, root)
		if _, err := submit(root, "slurm/run.sh"); err != nil {
			t.Fatal(err)
		}
		if commits[i], err = headCommit(root); err != nil {
			t.Fatal(err)
		}
	}
	return root, fake, dir, commits
}

func TestSnapshotsAreSizedAndJudged(t *testing.T) {
	for name, tg := range testTargets(t) {
		root, fake, dir, commits := submitTwice(t, tg, "exclude = [\"__pycache__\"]\n")
		write(t, filepath.Join(fake, "sacct"), "100|COMPLETED\n101|RUNNING\n")
		write(t, filepath.Join(dir, commits[0], "results.csv"), "r\n")
		write(t, filepath.Join(dir, commits[0], "src", "__pycache__", "a.pyc"), "c\n")
		write(t, filepath.Join(dir, commits[0], "slurm-100.out"), "log\n")
		write(t, filepath.Join(dir, commits[0], "src", "slurm-1.out"), "nested\n")
		cfg, err := loadConfig(root, "")
		if err != nil {
			t.Fatal(err)
		}
		infos, err := loadSnapshots(root, cfg, dir)
		if err != nil || len(infos) != 2 {
			t.Fatalf("%s: infos = %v, %v", name, infos, err)
		}
		by := map[string]snapshotInfo{}
		for _, s := range infos {
			by[s.Commit] = s
		}
		old, cur := by[commits[0]], by[commits[1]]
		if old.State != stateStray || !reflect.DeepEqual(old.Stray, []string{"results.csv", "src/slurm-1.out"}) {
			t.Errorf("%s: old = %v %v", name, old.State, old.Stray)
		}
		if cur.State != stateActive || cur.Active != 1 || cur.Total != 1 {
			t.Errorf("%s: current = %+v", name, cur)
		}
		if old.KB < 190 || cur.KB >= 100 {
			t.Errorf("%s: sizes old %dK, current %dK: hardlinked files must count once", name, old.KB, cur.KB)
		}
	}
}

func TestSnapshotsFailWhenSlurmDoes(t *testing.T) {
	for name, tg := range testTargets(t) {
		root, fake, _, _ := submitTwice(t, tg, "")
		write(t, filepath.Join(fake, "fail-squeue"), "")
		var out bytes.Buffer
		if err := runSnapshots(&out, root, "", false, nil); err == nil || !strings.Contains(err.Error(), "squeue") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestRunSnapshotsPrintsPlainAndJSON(t *testing.T) {
	for name, tg := range testTargets(t) {
		root, fake, dir, commits := submitTwice(t, tg, "")
		write(t, filepath.Join(fake, "sacct"), "100|COMPLETED\n101|COMPLETED\n")
		write(t, filepath.Join(dir, commits[1], "results.csv"), "r\n")
		var out bytes.Buffer
		if err := runSnapshots(&out, root, "", false, nil); err != nil {
			t.Fatal(err)
		}
		plain := out.String()
		if !strings.Contains(plain, commits[0][:8]+"  ") || !strings.Contains(plain, "  0/1  final\n") ||
			!strings.Contains(plain, "  0/1  stray\n    results.csv\n") {
			t.Errorf("%s: plain = %q", name, plain)
		}
		out.Reset()
		if err := runSnapshots(&out, root, "", true, nil); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), `"commit":"`+commits[1]+`"`) || !strings.Contains(out.String(), `"stray":["results.csv"]`) {
			t.Errorf("%s: json = %q", name, out.String())
		}
		if err := runSnapshots(&out, root, "", false, []string{"x"}); err == nil {
			t.Errorf("%s: arguments must be a usage error", name)
		}
	}
}

func drop(root string, ask confirmer, args ...string) (string, error) {
	var out bytes.Buffer
	err := runDrop(&out, root, "", ask, false, args)
	return out.String(), err
}

func TestDropFollowsTheRules(t *testing.T) {
	for name, tg := range testTargets(t) {
		root, fake, dir, commits := submitTwice(t, tg, "")
		write(t, filepath.Join(fake, "sacct"), "100|COMPLETED\n101|RUNNING\n")
		if _, err := drop(root, confirmer{Yes: true}, commits[1][:10]); err == nil || !strings.Contains(err.Error(), "active") {
			t.Errorf("%s: dropping an active snapshot = %v", name, err)
		}
		if _, err := drop(root, confirmer{Yes: true}, "0000"); err == nil || !strings.Contains(err.Error(), "matches") {
			t.Errorf("%s: unknown prefix = %v", name, err)
		}
		if _, err := drop(root, confirmer{Yes: true}); err == nil {
			t.Errorf("%s: no commit must be a usage error", name)
		}
		if _, err := drop(root, confirmer{}, commits[0][:10]); err == nil || !exists(filepath.Join(dir, commits[0])) {
			t.Errorf("%s: unconfirmed drop = %v", name, err)
		}
		declined := confirmer{Tty: true, In: strings.NewReader("n\n"), Out: &bytes.Buffer{}}
		if _, err := drop(root, declined, commits[0][:10]); err == nil || !exists(filepath.Join(dir, commits[0])) {
			t.Errorf("%s: declined drop = %v", name, err)
		}
		write(t, filepath.Join(dir, commits[0], "results.csv"), "r\n")
		out, err := drop(root, confirmer{Yes: true}, commits[0][:10])
		if err != nil || out != "dropped "+commits[0][:8]+" on h\n" {
			t.Errorf("%s: drop = %q, %v", name, out, err)
		}
		if exists(filepath.Join(dir, commits[0])) || !exists(filepath.Join(dir, commits[1])) {
			t.Errorf("%s: wrong snapshots left", name)
		}
	}
}

func TestDropRefusesAnAmbiguousPrefix(t *testing.T) {
	for name, tg := range testTargets(t) {
		root := jobRepo(t, tg, "")
		setupFakeSlurm(t)
		dir, err := tg.resolve()
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range []string{"1", "2"} {
			if err := os.MkdirAll(filepath.Join(dir, strings.Repeat("a", 39)+n), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := drop(root, confirmer{Yes: true}, "aaaa"); err == nil || !strings.Contains(err.Error(), "2 snapshots") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestDeleteSnapshotsRefusesAnythingButAHash(t *testing.T) {
	cfg := Config{Target: Target{Dir: t.TempDir()}}
	if err := deleteSnapshots(cfg, cfg.Target.Dir, []string{"../work"}); err == nil {
		t.Error("a path must not be deleted")
	}
}
