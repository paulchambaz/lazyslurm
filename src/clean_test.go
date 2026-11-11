package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const cleanKey = "clean = [\"outputs/logs/{job}\"]\n"

func cleanRepo(t *testing.T, tg Target, sacct string) (root, dir, fake string) {
	t.Helper()
	fake = setupFakeSlurm(t)
	root = jobRepo(t, tg, cleanKey)
	if _, err := submit(root, "slurm/run.sh"); err != nil {
		t.Fatal(err)
	}
	dir, err := tg.resolve()
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "keep", "outputs", "logs", "100", "a.txt"), "a\n")
	write(t, filepath.Join(fake, "sacct"), sacct)
	if _, err := pull(root); err != nil {
		t.Fatal(err)
	}
	return root, dir, fake
}

func clean(root string, ask confirmer) (string, error) {
	var out bytes.Buffer
	err := runClean(&out, root, "", ask, false, nil)
	return out.String(), err
}

func TestCleanDeletesFinishedPulledOutputs(t *testing.T) {
	for name, tg := range testTargets(t) {
		for _, sacct := range []string{"100|COMPLETED\n", "100|FAILED\n", "100_1|COMPLETED\n100_2|CANCELLED\n"} {
			root, dir, _ := cleanRepo(t, tg, sacct)
			out, err := clean(root, confirmer{Yes: true})
			if err != nil || !strings.HasPrefix(out, "cleaned 1 runs on h (") {
				t.Fatalf("%s %q: clean = %q, %v", name, sacct, out, err)
			}
			if exists(filepath.Join(dir, "keep", "outputs", "logs", "100")) {
				t.Errorf("%s %q: the run is still on the target", name, sacct)
			}
			if !exists(filepath.Join(root, "outputs", "logs", "100", "a.txt")) {
				t.Errorf("%s %q: the local copy is gone", name, sacct)
			}
		}
	}
}

func TestCleanKeepsWhatIsNotSafe(t *testing.T) {
	tests := []struct {
		name  string
		sacct string
		setup func(t *testing.T, root, dir string)
	}{
		{"running job", "100|RUNNING\n", nil},
		{"array with a running task", "100_1|COMPLETED\n100_2|RUNNING\n", nil},
		{"pending task", "100_1|COMPLETED\n100_[2-5]|PENDING\n", nil},
		{"file not pulled", "100|COMPLETED\n", func(t *testing.T, _, dir string) {
			write(t, filepath.Join(dir, "keep", "outputs", "logs", "100", "late.txt"), "x\n")
		}},
		{"file changed on the target", "100|COMPLETED\n", func(t *testing.T, _, dir string) {
			write(t, filepath.Join(dir, "keep", "outputs", "logs", "100", "a.txt"), "changed on the target\n")
		}},
		{"file removed locally", "100|COMPLETED\n", func(t *testing.T, root, _ string) {
			if err := os.Remove(filepath.Join(root, "outputs", "logs", "100", "a.txt")); err != nil {
				t.Fatal(err)
			}
		}},
		{"run not pulled at all", "100|COMPLETED\n", func(t *testing.T, root, _ string) {
			if err := os.RemoveAll(filepath.Join(root, "outputs")); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for name, tg := range testTargets(t) {
		for _, tt := range tests {
			root, dir, _ := cleanRepo(t, tg, tt.sacct)
			if tt.setup != nil {
				tt.setup(t, root, dir)
			}
			out, err := clean(root, confirmer{Yes: true})
			if err != nil || out != "nothing to clean\n" {
				t.Errorf("%s %s: clean = %q, %v", name, tt.name, out, err)
			}
			if !exists(filepath.Join(dir, "keep", "outputs", "logs", "100", "a.txt")) {
				t.Errorf("%s %s: the run was deleted", name, tt.name)
			}
		}
	}
}

func TestCleanLeavesUnknownIdsAndOtherDirectories(t *testing.T) {
	for name, tg := range testTargets(t) {
		root, dir, _ := cleanRepo(t, tg, "100|COMPLETED\n")
		logs := filepath.Join(dir, "keep", "outputs", "logs")
		write(t, filepath.Join(logs, "999", "a.txt"), "old\n")
		write(t, filepath.Join(logs, "notes", "a.txt"), "mine\n")
		write(t, filepath.Join(logs, "100.txt"), "file\n")
		write(t, filepath.Join(dir, "keep", "outputs", "weights", "100", "w.bin"), "w\n")
		if _, err := clean(root, confirmer{Yes: true}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, p := range []string{"999/a.txt", "notes/a.txt", "100.txt", "../weights/100/w.bin"} {
			if !exists(filepath.Join(logs, p)) {
				t.Errorf("%s: %s was deleted", name, p)
			}
		}
	}
}

func TestCleanIgnoresASymlinkedRun(t *testing.T) {
	for name, tg := range testTargets(t) {
		root, dir, _ := cleanRepo(t, tg, "100|COMPLETED\n")
		logs := filepath.Join(dir, "keep", "outputs", "logs")
		if err := os.RemoveAll(filepath.Join(logs, "100")); err != nil {
			t.Fatal(err)
		}
		elsewhere := t.TempDir()
		write(t, filepath.Join(elsewhere, "precious"), "p\n")
		if err := os.Symlink(elsewhere, filepath.Join(logs, "100")); err != nil {
			t.Fatal(err)
		}
		if out, err := clean(root, confirmer{Yes: true}); err != nil || out != "nothing to clean\n" {
			t.Errorf("%s: clean = %q, %v", name, out, err)
		}
		if !exists(filepath.Join(elsewhere, "precious")) {
			t.Errorf("%s: the symlink target lost its files", name)
		}
	}
}

func TestCleanIgnoresAParentLeavingKeep(t *testing.T) {
	for name, tg := range testTargets(t) {
		root, dir, _ := cleanRepo(t, tg, "100|COMPLETED\n")
		outputs := filepath.Join(dir, "keep", "outputs")
		elsewhere := filepath.Join(t.TempDir(), "outputs")
		if err := os.Rename(outputs, elsewhere); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(elsewhere, outputs); err != nil {
			t.Fatal(err)
		}
		if out, err := clean(root, confirmer{Yes: true}); err != nil || out != "nothing to clean\n" {
			t.Errorf("%s: clean = %q, %v", name, out, err)
		}
		if !exists(filepath.Join(elsewhere, "logs", "100", "a.txt")) {
			t.Errorf("%s: a run outside keep was deleted", name)
		}
	}
}

func TestCleanDeletesNothingWhenSlurmFails(t *testing.T) {
	for name, tg := range testTargets(t) {
		root, dir, fake := cleanRepo(t, tg, "100|COMPLETED\n")
		write(t, filepath.Join(fake, "fail"), "")
		if _, err := clean(root, confirmer{Yes: true}); err == nil || !strings.Contains(err.Error(), "boom") {
			t.Errorf("%s: err = %v", name, err)
		}
		if !exists(filepath.Join(dir, "keep", "outputs", "logs", "100", "a.txt")) {
			t.Errorf("%s: the run was deleted", name)
		}
	}
}

func TestCleanNeedsConfirmation(t *testing.T) {
	for name, tg := range testTargets(t) {
		root, dir, _ := cleanRepo(t, tg, "100|COMPLETED\n")
		_, err := clean(root, confirmer{})
		if _, ok := err.(confirmError); !ok {
			t.Errorf("%s: err = %v, want a confirmError", name, err)
		}
		if !exists(filepath.Join(dir, "keep", "outputs", "logs", "100")) {
			t.Errorf("%s: deleted without a confirmation", name)
		}
		out, err := clean(root, confirmer{Tty: true, In: strings.NewReader("n\n"), Out: &bytes.Buffer{}})
		if err == nil || out != "" || !exists(filepath.Join(dir, "keep", "outputs", "logs", "100")) {
			t.Errorf("%s: declined clean = %q, %v", name, out, err)
		}
	}
}

func TestCleanNeedsTheKeyAndNoArguments(t *testing.T) {
	setupFakeSlurm(t)
	root := jobRepo(t, Target{Dir: filepath.Join(t.TempDir(), "t")}, "")
	if _, err := clean(root, confirmer{Yes: true}); err == nil || !strings.Contains(err.Error(), "no clean key") {
		t.Errorf("err = %v", err)
	}
	var out bytes.Buffer
	if err := runClean(&out, root, "", confirmer{Yes: true}, false, []string{"x"}); err == nil {
		t.Error("an argument is accepted")
	} else if _, ok := err.(usageError); !ok {
		t.Errorf("err = %v, want a usageError", err)
	}
}

func TestCleanPrintsJSON(t *testing.T) {
	root, _, _ := cleanRepo(t, Target{Dir: filepath.Join(t.TempDir(), "t")}, "100|COMPLETED\n")
	var out bytes.Buffer
	if err := runClean(&out, root, "", confirmer{Yes: true}, true, nil); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.HasPrefix(got, `{"host":"h","paths":["outputs/logs/100"],"kb":`) {
		t.Errorf("json = %q", got)
	}
	out.Reset()
	if err := runClean(&out, root, "", confirmer{Yes: true}, true, nil); err != nil || !strings.HasPrefix(out.String(), `{"host":"h","paths":[],"kb":0}`) {
		t.Errorf("empty json = %q, %v", out.String(), err)
	}
}

func TestDeleteCleanRefusesAForeignPath(t *testing.T) {
	cfg := Config{Target: Target{Dir: t.TempDir()}, Clean: []string{"outputs/logs/{job}"}}
	for _, p := range []string{"outputs", "outputs/logs/abc", "outputs/logs/1/2", "../1", "other/1"} {
		if err := deleteClean(cfg, []cleanItem{{Path: p}}); err == nil {
			t.Errorf("%q accepted", p)
		}
	}
}

func TestNotPulledReadsRsyncItemizedChanges(t *testing.T) {
	out := "sending incremental file list\n.d..t...... outputs/logs/1/\n.f...p..... outputs/logs/1/a\n" +
		">f+++++++++ outputs/logs/2/b\ncd+++++++++ outputs/logs/3/\n*deleting   outputs/logs/4/c\n" +
		"created directory x\n\nsent 1 bytes  received 2 bytes\n"
	got := notPulled(out, []string{"outputs/logs/1", "outputs/logs/2", "outputs/logs/3", "outputs/logs/4", "outputs/logs/5"})
	for _, p := range []string{"outputs/logs/2", "outputs/logs/3", "outputs/logs/4"} {
		if !got[p] {
			t.Errorf("%s is not reported as left", p)
		}
	}
	if got["outputs/logs/1"] || got["outputs/logs/5"] {
		t.Errorf("only attribute changes must not count: %v", got)
	}
}

func TestCleanKeyIsValidated(t *testing.T) {
	for _, key := range []string{`["outputs/logs/{job}/x"]`, `["outputs/{job}/logs/{job}"]`, `["/outputs/{job}"]`,
		`["other/{job}"]`, `["{job}"]`, `["outputs/../x/{job}"]`, `["outputs/logs"]`} {
		root := jobRepo(t, Target{Dir: filepath.Join(t.TempDir(), "t")}, "clean = "+key+"\n")
		if _, err := loadConfig(root, ""); err == nil || !strings.Contains(err.Error(), "clean:") {
			t.Errorf("clean = %s: err = %v", key, err)
		}
	}
	root := jobRepo(t, Target{Dir: filepath.Join(t.TempDir(), "t")}, "clean = [\"outputs//logs/./{job}\", \"outputs/{job}\"]\n")
	cfg, err := loadConfig(root, "")
	if err != nil || len(cfg.Clean) != 2 || cfg.Clean[0] != "outputs/logs/{job}" || cfg.Clean[1] != "outputs/{job}" {
		t.Errorf("clean = %v, %v", cfg.Clean, err)
	}
}

func TestHooksKnowTheirHost(t *testing.T) {
	var out bytes.Buffer
	if err := localHook(&out, "post_pull", t.TempDir(), `echo "$CLUSTER"`, hookEnv("abc", false, "pfcalcul")); err != nil || out.String() != "pfcalcul\n" {
		t.Errorf("CLUSTER = %q, %v", out.String(), err)
	}
}

func TestCleanKeyInTheUI(t *testing.T) {
	setupFakeSlurm(t)
	u := newTestTUI(t, scriptRepo(t, ""))
	u.clean()
	if u.pop != nil || !strings.Contains(u.logText(), "no clean key") {
		t.Errorf("popup %v, log %q", u.pop, u.logText())
	}
}

func TestCleanAsksThenDeletesInTheUI(t *testing.T) {
	tg := Target{Dir: filepath.Join(t.TempDir(), "t")}
	root, dir, _ := cleanRepo(t, tg, "100|COMPLETED\n")
	u := newTestTUI(t, root)
	u.start("refresh", nil, nil)
	u.settle(t)
	u.clean()
	u.settle(t)
	if u.pop == nil || u.pop.kind != confirmPopup || !strings.Contains(u.pop.text, "delete 1 runs on h") {
		t.Fatalf("popup = %v", u.pop)
	}
	if !strings.Contains(u.logText(), "outputs/logs/100") || !exists(filepath.Join(dir, "keep", "outputs", "logs", "100")) {
		t.Fatalf("listed before the answer: log %q", u.logText())
	}
	u.yes()
	u.settle(t)
	if exists(filepath.Join(dir, "keep", "outputs", "logs", "100")) || !strings.Contains(u.logText(), "cleaned 1 runs on h") {
		t.Errorf("after yes: log %q", u.logText())
	}
}
