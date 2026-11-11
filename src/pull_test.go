package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func pull(root string) (string, error) {
	var out bytes.Buffer
	err := runPull(&out, root, "", false, nil)
	return out.String(), err
}

func TestPullFetchesPathsAndLogsInOrder(t *testing.T) {
	for name, tg := range testTargets(t) {
		root := jobRepo(t, tg, "pre_pull = 'echo ran > pre_ran'\npost_pull = 'cp outputs/logs/sub/a.txt saw.txt'\n")
		dir, err := tg.resolve()
		if err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(dir, "work", "keep"), "")
		write(t, filepath.Join(dir, "keep", "outputs", "logs", "sub", "a.txt"), "log\n")
		write(t, filepath.Join(dir, "keep", "outputs", "weights", "w.bin"), "w\n")
		write(t, filepath.Join(dir, "abc", "slurm-5.out"), "partial\n")
		write(t, filepath.Join(dir, "abc", "slurm-5.err"), "e\n")
		write(t, filepath.Join(dir, "abc", "other.txt"), "no\n")
		write(t, filepath.Join(dir, "abc", "src", "slurm-6.out"), "deep\n")
		write(t, filepath.Join(dir, "work", "slurm-9.out"), "work\n")
		write(t, filepath.Join(root, "outputs", "logs", "local.txt"), "mine\n")
		out, err := pull(root)
		if err != nil || out != "pulled 1 paths from h\n" {
			t.Fatalf("%s: pull = %q, %v", name, out, err)
		}
		if !exists(filepath.Join(dir, "work", "pre_ran")) {
			t.Errorf("%s: pre_pull did not run in work/", name)
		}
		for path, want := range map[string]string{
			"outputs/logs/sub/a.txt":      "log\n",
			"outputs/logs/local.txt":      "mine\n",
			"saw.txt":                     "log\n",
			".slurm/logs/abc/slurm-5.out": "partial\n",
			".slurm/logs/abc/slurm-5.err": "e\n",
		} {
			if got, _ := os.ReadFile(filepath.Join(root, path)); string(got) != want {
				t.Errorf("%s: %s = %q, want %q", name, path, got, want)
			}
		}
		for _, path := range []string{"outputs/weights", "outputs/missing", ".slurm/logs/abc/other.txt",
			".slurm/logs/abc/src", ".slurm/logs/work", ".slurm/logs/keep"} {
			if exists(filepath.Join(root, path)) {
				t.Errorf("%s: %s must not be pulled", name, path)
			}
		}
	}
}

func TestFailingPrePullPullsNothing(t *testing.T) {
	for name, tg := range testTargets(t) {
		root := jobRepo(t, tg, "pre_pull = 'exit 3'\npost_pull = 'touch post_ran'\n")
		dir, err := tg.resolve()
		if err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(dir, "work", "keep"), "")
		write(t, filepath.Join(dir, "keep", "outputs", "logs", "a.txt"), "log\n")
		if _, err := pull(root); err == nil || !strings.Contains(err.Error(), "pre_pull") {
			t.Errorf("%s: err = %v", name, err)
		}
		for _, path := range []string{"outputs", ".slurm", "post_ran"} {
			if exists(filepath.Join(root, path)) {
				t.Errorf("%s: %s exists after a failed pre_pull", name, path)
			}
		}
	}
}

func TestPullTakesNoArguments(t *testing.T) {
	root := jobRepo(t, testTargets(t)["local"], "")
	if err := runPull(&bytes.Buffer{}, root, "", false, []string{"x"}); err == nil {
		t.Error("pull x must fail")
	}
}
