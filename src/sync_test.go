package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func gitRun(t *testing.T, root string, args ...string) {
	t.Helper()
	if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func syncRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitRun(t, root, "init", "-q")
	write(t, filepath.Join(root, ".gitignore"), "ignored.txt\n")
	write(t, filepath.Join(root, "src", "a.py"), "a1\n")
	write(t, filepath.Join(root, "src", "b.py"), "b1\n")
	write(t, filepath.Join(root, "src", "gone.py"), "gone\n")
	write(t, filepath.Join(root, "src", "ignored.txt"), "no\n")
	write(t, filepath.Join(root, "other", "z.py"), "z\n")
	write(t, filepath.Join(root, "pyproject.toml"), "p\n")
	gitRun(t, root, "add", "-A")
	if err := os.Remove(filepath.Join(root, "src", "gone.py")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "src", "new.py"), "new\n")
	write(t, filepath.Join(root, "src", "__pycache__", "x.pyc"), "x\n")
	return root
}

func syncConfig(tg Target) Config {
	return Config{
		Host:    "h",
		Target:  tg,
		Push:    []string{"src", "pyproject.toml"},
		Exclude: []string{"__pycache__"},
		Keep:    []string{".venv", "outputs"},
	}
}

func pushOnce(t *testing.T, root string, cfg Config) (dir string, sent, removed int, err error) {
	t.Helper()
	dir, err = cfg.Target.resolve()
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := cfg.Target.lock(dir)
	if err != nil {
		t.Fatal(err)
	}
	sent, removed, err = push(root, cfg, dir)
	if uerr := unlock(); uerr != nil {
		t.Fatal(uerr)
	}
	return dir, sent, removed, err
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func TestFileSet(t *testing.T) {
	root := syncRepo(t)
	got, err := fileSet(root, syncConfig(Target{}))
	want := []string{"pyproject.toml", "src/a.py", "src/b.py", "src/new.py"}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("fileSet = %v, %v, want %v", got, err, want)
	}
}

func TestExcluded(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"src/a.py", false},
		{"src/__pycache__/x.pyc", true},
		{"__pycache__", true},
		{"src/.git/config", true},
		{"src/x.pyc", true},
		{"src/pycache", false},
	}
	for _, tt := range tests {
		if got := excluded(tt.path, []string{"__pycache__", "*.pyc"}); got != tt.want {
			t.Errorf("excluded(%q) = %v", tt.path, got)
		}
	}
}

func TestPushMirrorsThePushSet(t *testing.T) {
	for name, tg := range testTargets(t) {
		root := syncRepo(t)
		cfg := syncConfig(tg)
		dir, sent, removed, err := pushOnce(t, root, cfg)
		if err != nil || sent != 4 || removed != 0 {
			t.Fatalf("%s: first push = %d, %d, %v", name, sent, removed, err)
		}
		work := filepath.Join(dir, "work")
		for _, f := range []string{"pyproject.toml", "src/a.py", "src/b.py", "src/new.py"} {
			if !exists(filepath.Join(work, f)) {
				t.Errorf("%s: %s is missing", name, f)
			}
		}
		for _, f := range []string{"other", "src/ignored.txt", "src/gone.py", "src/__pycache__"} {
			if exists(filepath.Join(work, f)) {
				t.Errorf("%s: %s must not be sent", name, f)
			}
		}
		for _, k := range cfg.Keep {
			if got, err := os.Readlink(filepath.Join(work, k)); err != nil || got != filepath.Join(dir, "keep", k) {
				t.Errorf("%s: keep link %s = %q, %v", name, k, got, err)
			}
		}
	}
}

func TestSecondPushSendsOnlyChangesAndPrunes(t *testing.T) {
	for name, tg := range testTargets(t) {
		root := syncRepo(t)
		cfg := syncConfig(tg)
		dir, _, _, err := pushOnce(t, root, cfg)
		if err != nil {
			t.Fatal(err)
		}
		work := filepath.Join(dir, "work")
		write(t, filepath.Join(work, "src", "__pycache__", "kept.pyc"), "k\n")
		write(t, filepath.Join(dir, "keep", "outputs", "r.txt"), "r\n")
		write(t, filepath.Join(root, "src", "sub", "c.py"), "c\n")
		if _, _, _, err := pushOnce(t, root, cfg); err != nil {
			t.Fatal(err)
		}
		before, err := os.Stat(filepath.Join(work, "pyproject.toml"))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{"src/b.py", "src/sub"} {
			if err := os.RemoveAll(filepath.Join(root, f)); err != nil {
				t.Fatal(err)
			}
		}
		write(t, filepath.Join(root, "src", "a.py"), "a22\n")
		_, sent, removed, err := pushOnce(t, root, cfg)
		if err != nil || sent != 3 || removed != 2 {
			t.Fatalf("%s: push = %d sent, %d removed, %v", name, sent, removed, err)
		}
		after, err := os.Stat(filepath.Join(work, "pyproject.toml"))
		if err != nil || !os.SameFile(before, after) {
			t.Errorf("%s: an unchanged file was replaced: %v", name, err)
		}
		if got, _ := os.ReadFile(filepath.Join(work, "src", "a.py")); string(got) != "a22\n" {
			t.Errorf("%s: a.py = %q", name, got)
		}
		for _, f := range []string{"src/b.py", "src/sub"} {
			if exists(filepath.Join(work, f)) {
				t.Errorf("%s: %s should have been pruned", name, f)
			}
		}
		for _, f := range []string{"src/__pycache__/kept.pyc", ".venv", "outputs"} {
			if !exists(filepath.Join(work, f)) {
				t.Errorf("%s: %s must survive a push", name, f)
			}
		}
		if !exists(filepath.Join(dir, "keep", "outputs", "r.txt")) {
			t.Errorf("%s: keep data was touched", name)
		}
	}
}

func TestPushRefusesARealFolderInPlaceOfAKeepLink(t *testing.T) {
	for name, tg := range testTargets(t) {
		root := syncRepo(t)
		dir, err := tg.resolve()
		if err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(dir, "work", ".venv", "f"), "real\n")
		_, _, _, err = pushOnce(t, root, syncConfig(tg))
		if err == nil || !strings.Contains(err.Error(), ".venv") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestSnapshotIsHardlinkedAndSurvivesTheNextPush(t *testing.T) {
	for name, tg := range testTargets(t) {
		root := syncRepo(t)
		cfg := syncConfig(tg)
		dir, _, _, err := pushOnce(t, root, cfg)
		if err != nil {
			t.Fatal(err)
		}
		const commit = "0123456789abcdef0123456789abcdef01234567"
		if err := snapshot(tg, dir, commit); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		snap := filepath.Join(dir, commit)
		w, err := os.Stat(filepath.Join(dir, "work", "src", "a.py"))
		if err != nil {
			t.Fatal(err)
		}
		s, err := os.Stat(filepath.Join(snap, "src", "a.py"))
		if err != nil || !os.SameFile(w, s) {
			t.Errorf("%s: snapshot file is not a hardlink: %v", name, err)
		}
		if got, err := os.Readlink(filepath.Join(snap, ".venv")); err != nil || got != filepath.Join(dir, "keep", ".venv") {
			t.Errorf("%s: snapshot keep link = %q, %v", name, got, err)
		}

		write(t, filepath.Join(root, "src", "a.py"), "a22\n")
		if _, _, _, err := pushOnce(t, root, cfg); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(filepath.Join(snap, "src", "a.py")); string(got) != "a1\n" {
			t.Errorf("%s: snapshot followed work/: %q", name, got)
		}
		if err := snapshot(tg, dir, commit); err != nil {
			t.Fatalf("%s: reuse: %v", name, err)
		}
		if got, _ := os.ReadFile(filepath.Join(snap, "src", "a.py")); string(got) != "a1\n" {
			t.Errorf("%s: reuse rewrote the snapshot: %q", name, got)
		}
	}
}

func TestSnapshotReplacesALeftoverTmp(t *testing.T) {
	for name, tg := range testTargets(t) {
		root := syncRepo(t)
		dir, _, _, err := pushOnce(t, root, syncConfig(tg))
		if err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(dir, "abc.tmp", "junk"), "j\n")
		if err := snapshot(tg, dir, "abc"); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if exists(filepath.Join(dir, "abc", "junk")) || exists(filepath.Join(dir, "abc.tmp")) {
			t.Errorf("%s: leftover tmp survived", name)
		}
	}
}
