package tests

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "lazyslurm")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	binary = filepath.Join(dir, "lazyslurm")
	if out, err := exec.Command("go", "build", "-o", binary, "../src").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build: %v\n%s", err, out)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func lazyslurm(t *testing.T, dir string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CLUSTER=")
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		return out.String(), errOut.String(), exit.ExitCode()
	}
	if err != nil {
		t.Fatal(err)
	}
	return out.String(), errOut.String(), 0
}

func repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	return dir
}

func commitAll(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{{"add", "-A"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "x"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func fakeSlurm(t *testing.T) string {
	t.Helper()
	bin, state := t.TempDir(), t.TempDir()
	scripts := map[string]string{
		"sbatch":   "#!/bin/sh\nn=$(cat \"$FAKE_SLURM/next\" 2>/dev/null || echo 100)\necho $((n + 1)) > \"$FAKE_SLURM/next\"\necho \"$n\"\n",
		"sacct":    "#!/bin/sh\ncat \"$FAKE_SLURM/sacct\" 2>/dev/null\nexit 0\n",
		"squeue":   "#!/bin/sh\ncat \"$FAKE_SLURM/squeue\" 2>/dev/null\nexit 0\n",
		"scontrol": "#!/bin/sh\ncat \"$FAKE_SLURM/scontrol\" 2>/dev/null || exit 1\n",
		"scancel":  "#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"$FAKE_SLURM/scancel.log\"\n",
		"srun":     "#!/bin/sh\nwhile [ \"${1#-}\" != \"$1\" ]; do shift; done\nexec \"$@\"\n",
	}
	for name, body := range scripts {
		write(t, filepath.Join(bin, name), body)
		if err := os.Chmod(filepath.Join(bin, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("FAKE_SLURM", state)
	return state
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestVersionPrintsTheVersionFile(t *testing.T) {
	want, err := os.ReadFile("../src/VERSION")
	if err != nil {
		t.Fatal(err)
	}
	out, _, code := lazyslurm(t, ".", "-V")
	if code != 0 || strings.TrimSpace(out) != "lazyslurm "+strings.TrimSpace(string(want)) {
		t.Errorf("-V = %q, exit %d", out, code)
	}
}

func TestHelpPrintsUsage(t *testing.T) {
	out, _, code := lazyslurm(t, ".", "-h")
	if code != 0 || !strings.HasPrefix(out, "usage: lazyslurm") {
		t.Errorf("-h = %q, exit %d", out, code)
	}
}

func TestUnknownFlagExitsWithUsageCode(t *testing.T) {
	if _, _, code := lazyslurm(t, ".", "-x"); code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
}

func TestUnknownCommandExitsWithUsageCode(t *testing.T) {
	_, stderr, code := lazyslurm(t, ".", "frobnicate")
	if code != 2 || !strings.Contains(stderr, "frobnicate") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
}

func TestStatusListsTheJournal(t *testing.T) {
	fakeSlurm(t)
	dir := repo(t)
	write(t, filepath.Join(dir, ".slurm", "config"), `
[default]
push = ["src"]
[hosts.a]
target = "/scratch/x"
`)
	write(t, filepath.Join(dir, ".slurm", "state", "jobs.jsonl"),
		`{"v":3,"time":"t1","host":"a","job":"1","script":"s.sh","commit":"0123456789abcdef"}`+"\n"+
			`{"v":3,"time":"t2","host":"a","job":"2","script":"s.sh","commit":"0123456789abcdef"}`+"\n")
	write(t, filepath.Join(dir, ".slurm", "state", "seen"), "a 1\n")

	out, _, code := lazyslurm(t, dir, "status")
	if code != 0 || out != "2  t2  s.sh  01234567\n" {
		t.Errorf("status = %q, exit %d", out, code)
	}
	out, _, _ = lazyslurm(t, dir, "status", "--all", "--json")
	if strings.Count(out, "\n") != 2 || !strings.HasPrefix(out, `{"host":"a","job":"2"`) {
		t.Errorf("status --all --json = %q", out)
	}
	_, stderr, code := lazyslurm(t, dir, "-H", "nope", "status")
	if code != 1 || !strings.Contains(stderr, "a") {
		t.Errorf("-H nope: exit %d, stderr %q", code, stderr)
	}
}

func TestStatusOutsideARepoFails(t *testing.T) {
	_, stderr, code := lazyslurm(t, t.TempDir(), "status")
	if code != 1 || !strings.Contains(stderr, "not a git repository") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
}

func TestStatusReportsABrokenConfig(t *testing.T) {
	dir := repo(t)
	write(t, filepath.Join(dir, ".slurm", "config"), "[default]\nbogus = 1\n")
	_, stderr, code := lazyslurm(t, dir, "status")
	if code != 1 || !strings.Contains(stderr, "bogus") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
}

func pushRepo(t *testing.T) (dir, target string) {
	t.Helper()
	dir, target = repo(t), filepath.Join(t.TempDir(), "target")
	write(t, filepath.Join(dir, ".slurm", "config"), "[default]\npush = [\"src\"]\n[hosts.a]\ntarget = \""+target+"\"\n")
	write(t, filepath.Join(dir, "src", "a.py"), "a\n")
	commitAll(t, dir)
	return dir, target
}

func TestPushSendsToALocalTarget(t *testing.T) {
	dir, target := pushRepo(t)
	out, stderr, code := lazyslurm(t, dir, "push")
	if code != 0 || out != "pushed 1 files to a (0 removed)\n" {
		t.Fatalf("push = %q, %q, exit %d", out, stderr, code)
	}
	if got, err := os.ReadFile(filepath.Join(target, "work", "src", "a.py")); err != nil || string(got) != "a\n" {
		t.Errorf("work/src/a.py = %q, %v", got, err)
	}
	out, _, code = lazyslurm(t, dir, "push", "--json")
	if code != 0 || out != `{"host":"a","files":1,"removed":0}`+"\n" {
		t.Errorf("push --json = %q, exit %d", out, code)
	}
}

func TestPushErrors(t *testing.T) {
	dir, _ := pushRepo(t)
	if _, _, code := lazyslurm(t, dir, "push", "extra"); code != 2 {
		t.Errorf("push extra: exit %d, want 2", code)
	}
	if _, stderr, code := lazyslurm(t, dir, "-H", "nope", "push"); code != 1 || !strings.Contains(stderr, "a") {
		t.Errorf("-H nope: exit %d, stderr %q", code, stderr)
	}
	if _, stderr, code := lazyslurm(t, repo(t), "push"); code != 1 || !strings.Contains(stderr, ".slurm/config") {
		t.Errorf("no config: exit %d, stderr %q", code, stderr)
	}
}

func TestSubmitStatusAndPullEndToEnd(t *testing.T) {
	fake := fakeSlurm(t)
	dir, target := pushRepo(t)
	write(t, filepath.Join(dir, "src", "run.sh"), "#!/bin/sh\n")
	commitAll(t, dir)

	_, stderr, code := lazyslurm(t, dir, "submit", "src/run.sh")
	if code != 3 || !strings.Contains(stderr, "--yes") {
		t.Fatalf("submit without --yes: exit %d, stderr %q", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(target, "work")); err == nil {
		t.Error("an unconfirmed submit touched the target")
	}
	out, stderr, code := lazyslurm(t, dir, "--yes", "submit", "--sbatch", "--time=1", "--note", "first", "src/run.sh", "-x")
	if code != 0 || out != "job 100 submitted on a\n" {
		t.Fatalf("submit = %q, %q, exit %d", out, stderr, code)
	}
	journal, err := os.ReadFile(filepath.Join(dir, ".slurm", "state", "jobs.jsonl"))
	if err != nil || !strings.Contains(string(journal), `"args":["-x"]`) || !strings.Contains(string(journal), `"note":"first"`) {
		t.Errorf("journal = %q, %v", journal, err)
	}

	out, _, _ = lazyslurm(t, dir, "status")
	if !strings.HasPrefix(out, "100  ") || strings.Contains(out, "COMPLETED") {
		t.Errorf("status before the job ends = %q", out)
	}
	write(t, filepath.Join(fake, "sacct"), "100|COMPLETED\n")
	out, _, _ = lazyslurm(t, dir, "status")
	if !strings.HasSuffix(out, "  1 COMPLETED  first\n") {
		t.Errorf("status after = %q", out)
	}
	if out, _, _ = lazyslurm(t, dir, "status"); out != "" {
		t.Errorf("a final job was shown twice: %q", out)
	}

	if out, stderr, code = lazyslurm(t, dir, "pull"); code != 0 || out != "pulled 0 paths from a, dropped 1 snapshots\n" {
		t.Errorf("pull = %q, %q, exit %d", out, stderr, code)
	}
	if _, _, code = lazyslurm(t, dir, "--yes", "submit", "nope.sh"); code != 1 {
		t.Errorf("bad script: exit %d, want 1", code)
	}
	if _, _, code = lazyslurm(t, dir, "submit"); code != 2 {
		t.Errorf("no script: exit %d, want 2", code)
	}
}

func TestVerbsEndToEnd(t *testing.T) {
	fake := fakeSlurm(t)
	dir, target := pushRepo(t)
	write(t, filepath.Join(dir, ".slurm", "config"), "[default]\npush = [\"src\"]\nsrun = \"--time=1\"\n[hosts.a]\ntarget = \""+target+"\"\n")
	write(t, filepath.Join(dir, "src", "run.sh"), "#!/bin/sh\n")
	commitAll(t, dir)
	if _, stderr, code := lazyslurm(t, dir, "--yes", "submit", "src/run.sh"); code != 0 {
		t.Fatalf("submit: exit %d, %q", code, stderr)
	}
	commit, _ := os.ReadDir(target)
	var snap string
	for _, e := range commit {
		if len(e.Name()) == 40 {
			snap = e.Name()
		}
	}
	if snap == "" {
		t.Fatal("no snapshot after submit")
	}

	out, _, code := lazyslurm(t, dir, "snapshots")
	if code != 0 || !strings.Contains(out, snap[:8]) || !strings.HasSuffix(out, "  1/1  active\n") {
		t.Errorf("snapshots = %q, exit %d", out, code)
	}
	if _, stderr, code := lazyslurm(t, dir, "drop", snap[:8]); code != 1 || !strings.Contains(stderr, "active") {
		t.Errorf("drop of an active snapshot: exit %d, %q", code, stderr)
	}

	write(t, filepath.Join(fake, "sacct"), "100|FAILED\n")
	if _, stderr, code := lazyslurm(t, dir, "retry"); code != 3 || !strings.Contains(stderr, "--yes") {
		t.Errorf("retry without --yes: exit %d, %q", code, stderr)
	}
	if out, _, code = lazyslurm(t, dir, "--yes", "retry"); code != 0 || out != "job 101 submitted on a (retry of 100)\n" {
		t.Errorf("retry = %q, exit %d", out, code)
	}

	write(t, filepath.Join(fake, "squeue"), "101|/somewhere\n")
	if _, stderr, code := lazyslurm(t, dir, "cancel", "101"); code != 3 || !strings.Contains(stderr, "--yes") {
		t.Errorf("cancel without --yes: exit %d, %q", code, stderr)
	}
	if out, _, code = lazyslurm(t, dir, "--yes", "cancel", "101"); code != 0 || out != "cancelled 101 on a\n" {
		t.Errorf("cancel = %q, exit %d", out, code)
	}
	if log, _ := os.ReadFile(filepath.Join(fake, "scancel.log")); string(log) != "101\n" {
		t.Errorf("scancel saw %q", log)
	}

	write(t, filepath.Join(target, snap, "slurm-100.out"), "hello\n")
	if out, _, code = lazyslurm(t, dir, "logs", "100"); code != 0 || out != "hello\n" {
		t.Errorf("logs = %q, exit %d", out, code)
	}

	write(t, filepath.Join(fake, "sacct"), "100|FAILED\n101|COMPLETED\n")
	write(t, filepath.Join(fake, "squeue"), "")
	if out, _, code = lazyslurm(t, dir, "--yes", "drop", snap); code != 0 || out != "dropped "+snap[:8]+" on a\n" {
		t.Errorf("drop = %q, exit %d", out, code)
	}

	if out, _, code = lazyslurm(t, dir, "exec", "--", "echo", "hi"); code != 0 || out != "hi\n" {
		t.Errorf("exec = %q, exit %d", out, code)
	}
	if _, _, code = lazyslurm(t, dir, "exec", "--", "exit 7"); code != 7 {
		t.Errorf("exec exit 7: exit %d", code)
	}
}

func TestCleanEndToEnd(t *testing.T) {
	fake := fakeSlurm(t)
	dir, target := repo(t), filepath.Join(t.TempDir(), "target")
	write(t, filepath.Join(dir, ".slurm", "config"), "[default]\npush = [\"src\"]\nkeep = [\"outputs\"]\n"+
		"pull = [\"outputs\"]\nclean = [\"outputs/{job}\"]\n[hosts.a]\ntarget = \""+target+"\"\n")
	write(t, filepath.Join(dir, "src", "run.sh"), "#!/bin/sh\n")
	commitAll(t, dir)

	if _, stderr, code := lazyslurm(t, dir, "--yes", "submit", "src/run.sh"); code != 0 {
		t.Fatalf("submit: exit %d, %q", code, stderr)
	}
	write(t, filepath.Join(target, "keep", "outputs", "100", "w.bin"), "weights\n")
	write(t, filepath.Join(target, "keep", "outputs", "200", "w.bin"), "not a job of ours\n")
	write(t, filepath.Join(fake, "sacct"), "100|COMPLETED\n")
	if _, stderr, code := lazyslurm(t, dir, "pull"); code != 0 {
		t.Fatalf("pull: exit %d, %q", code, stderr)
	}

	if _, stderr, code := lazyslurm(t, dir, "clean"); code != 3 || !strings.Contains(stderr, "--yes") {
		t.Errorf("clean without --yes: exit %d, %q", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(target, "keep", "outputs", "100")); err != nil {
		t.Fatal("an unconfirmed clean deleted the run")
	}
	if _, _, code := lazyslurm(t, dir, "clean", "x"); code != 2 {
		t.Errorf("clean with an argument: exit %d, want 2", code)
	}
	out, stderr, code := lazyslurm(t, dir, "--yes", "clean")
	if code != 0 || !strings.HasPrefix(out, "cleaned 1 runs on a (") || !strings.Contains(stderr, "outputs/100") {
		t.Fatalf("clean = %q, %q, exit %d", out, stderr, code)
	}
	if _, err := os.Stat(filepath.Join(target, "keep", "outputs", "100")); err == nil {
		t.Error("the run is still on the target")
	}
	if _, err := os.Stat(filepath.Join(target, "keep", "outputs", "200", "w.bin")); err != nil {
		t.Error("a run unknown to the journal was deleted")
	}
	if got, err := os.ReadFile(filepath.Join(dir, "outputs", "100", "w.bin")); err != nil || string(got) != "weights\n" {
		t.Errorf("local copy = %q, %v", got, err)
	}
	if out, _, code = lazyslurm(t, dir, "--yes", "clean"); code != 0 || out != "nothing to clean\n" {
		t.Errorf("second clean = %q, exit %d", out, code)
	}

	write(t, filepath.Join(dir, ".slurm", "config"), "[default]\npush = [\"src\"]\n[hosts.a]\ntarget = \""+target+"\"\n")
	if _, stderr, code := lazyslurm(t, dir, "--yes", "clean"); code != 1 || !strings.Contains(stderr, "no clean key") {
		t.Errorf("clean without a key: exit %d, %q", code, stderr)
	}
}
