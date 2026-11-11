package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jesseduffield/gocui"
)

func newTestTUI(t *testing.T, root string) *tui {
	t.Helper()
	hosts, host, err := hostList(root, "")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(root, host)
	if err != nil {
		t.Fatal(err)
	}
	g, err := gocui.NewGui(gocui.NewGuiOpts{Headless: true, Width: 120, Height: 36})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(g.Close)
	u := newTUI(g, root, hosts, host, cfg)
	u.post = func(f func()) { f() }
	u.log.wake = nil
	t.Cleanup(func() { hookOut = os.Stderr })
	hookOut = u.log
	return u
}

func (u *tui) settle(t *testing.T) {
	t.Helper()
	u.wg.Wait()
	if err := u.layout(u.g); err != nil {
		t.Fatal(err)
	}
}

func (u *tui) logText() string {
	return strings.Join(u.log.tail(logKept), "\n")
}

func scriptRepo(t *testing.T, extra string) string {
	t.Helper()
	return jobRepo(t, Target{Dir: filepath.Join(t.TempDir(), "t")}, "scripts = [\"slurm/run.sh\"]\n"+extra)
}

func TestLayoutDrawsEveryView(t *testing.T) {
	setupFakeSlurm(t)
	u := newTestTUI(t, scriptRepo(t, ""))
	if err := u.layout(u.g); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"hosts", "jobs", "snaps", mainView, logView, optionsView} {
		if _, err := u.g.View(name); err != nil {
			t.Errorf("view %s: %v", name, err)
		}
	}
	v, _ := u.g.View("hosts")
	if !strings.Contains(v.Buffer(), "* h") {
		t.Errorf("hosts view = %q", v.Buffer())
	}
	if jobs, _ := u.g.View("jobs"); u.g.CurrentView() != jobs {
		t.Errorf("current view = %v, want jobs", u.g.CurrentView())
	}
	if len(v.FrameRunes) != 6 || v.FrameRunes[2] != '╭' {
		t.Errorf("frame = %q, want rounded corners", string(v.FrameRunes))
	}
	if o, _ := u.g.View(optionsView); !strings.Contains(o.Buffer(), "Help: ?") {
		t.Errorf("options = %q", o.Buffer())
	}
}

func TestRefreshShowsJobsAndSnapshotsAndKeysMove(t *testing.T) {
	setupFakeSlurm(t)
	root := scriptRepo(t, "")
	for range 2 {
		if _, err := submit(root, "slurm/run.sh"); err != nil {
			t.Fatal(err)
		}
	}
	u := newTestTUI(t, root)
	u.start("refresh", nil, nil)
	u.settle(t)
	jobs, _ := u.g.View("jobs")
	if !strings.Contains(jobs.Buffer(), "101") || !strings.Contains(jobs.Buffer(), "100") {
		t.Errorf("jobs view = %q", jobs.Buffer())
	}
	snaps, _ := u.g.View("snaps")
	if !strings.Contains(snaps.Buffer(), "active") {
		t.Errorf("snaps view = %q", snaps.Buffer())
	}
	u.focusOn(jobsPanel)
	u.move(1)
	u.settle(t)
	if main, _ := u.g.View(mainView); !strings.Contains(main.Buffer(), "job       100") {
		t.Errorf("main view = %q", main.Buffer())
	}
	u.cycle(1)
	if u.m.focus != snapsPanel {
		t.Errorf("focus = %d", u.m.focus)
	}
	u.cycle(1)
	if u.m.focus != hostsPanel {
		t.Errorf("focus = %d, want a wrap to hosts", u.m.focus)
	}
}

func TestCancelAsksThenRuns(t *testing.T) {
	fake := setupFakeSlurm(t)
	root := scriptRepo(t, "")
	if _, err := submit(root, "slurm/run.sh"); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(fake, "squeue"), "100|/x\n")
	u := newTestTUI(t, root)
	u.start("refresh", nil, nil)
	u.settle(t)
	u.focusOn(jobsPanel)
	u.cancel()
	if u.pop == nil || u.pop.kind != confirmPopup {
		t.Fatalf("popup = %v", u.pop)
	}
	u.settle(t)
	if p, _ := u.g.View(popupView); !strings.Contains(p.Buffer(), "cancel job 100 on h? [y/N]") || u.g.CurrentView() != p {
		t.Errorf("popup view = %q", p.Buffer())
	}
	u.dismiss()
	u.settle(t)
	if _, err := u.g.View(popupView); err == nil {
		t.Error("popup still drawn after n")
	}
	if exists(filepath.Join(fake, "scancel.log")) {
		t.Fatal("scancel ran before the answer")
	}
	u.cancel()
	u.yes()
	u.settle(t)
	if log, _ := os.ReadFile(filepath.Join(fake, "scancel.log")); string(log) != "100\n" {
		t.Errorf("scancel saw %q", log)
	}
	if !strings.Contains(u.logText(), "cancelled 100 on h") {
		t.Errorf("log = %q", u.logText())
	}
}

func TestBusyBlocksASecondTask(t *testing.T) {
	setupFakeSlurm(t)
	u := newTestTUI(t, scriptRepo(t, ""))
	u.m.busy = true
	u.push()
	u.wg.Wait()
	if !strings.Contains(u.logText(), "busy: push not started") {
		t.Errorf("log = %q", u.logText())
	}
	if u.cancel(); u.pop != nil {
		t.Error("a popup opened while busy")
	}
}

func TestSubmitFlowAsksEverythingThenSubmits(t *testing.T) {
	fake := setupFakeSlurm(t)
	root := scriptRepo(t, "")
	u := newTestTUI(t, root)
	u.submit()
	answers := []string{"", "--a 1 b", "--time=2", "my note"}
	for i, answer := range answers {
		u.settle(t)
		p, err := u.g.View(popupView)
		if err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		p.TextArea.TypeString(answer)
		if err := u.accept(p); err != nil {
			t.Fatal(err)
		}
	}
	if u.pop == nil || u.pop.kind != confirmPopup {
		t.Fatalf("no confirmation after the inputs: %v", u.pop)
	}
	u.yes()
	u.settle(t)
	jobs, err := readJournal(root)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("journal = %v, %v", jobs, err)
	}
	j := jobs[0]
	if j.Script != "slurm/run.sh" || strings.Join(j.Args, " ") != "--a 1 b" || j.Sbatch != "--time=2" || j.Note != "my note" {
		t.Errorf("job = %+v", j)
	}
	if !exists(filepath.Join(fake, "sbatch.log")) || len(u.m.jobs) != 1 {
		t.Errorf("sbatch not run or list not refreshed: %d jobs", len(u.m.jobs))
	}
}

func TestHookOutputAndResultsGoToTheLogButNotTheCommands(t *testing.T) {
	setupFakeSlurm(t)
	u := newTestTUI(t, scriptRepo(t, "pre_push = \"echo hooked\"\n"))
	u.push()
	u.settle(t)
	text := u.logText()
	for _, want := range []string{"hooked", "pushed 2 files to h"} {
		if !strings.Contains(text, want) {
			t.Errorf("log lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "$ ") {
		t.Errorf("log shows commands:\n%s", text)
	}
}

func TestFilterPopupAndLogsOfAJob(t *testing.T) {
	fake := setupFakeSlurm(t)
	root := scriptRepo(t, "")
	if _, err := submit(root, "slurm/run.sh"); err != nil {
		t.Fatal(err)
	}
	logFile := filepath.Join(t.TempDir(), "out")
	write(t, logFile, "line one\nline two\n")
	write(t, filepath.Join(fake, "scontrol"), "   StdOut="+logFile+"\n   StdErr="+logFile+"\n")
	write(t, filepath.Join(fake, "sacct"), "100_1|COMPLETED\n100_2|FAILED\n")
	u := newTestTUI(t, root)
	u.start("refresh", nil, nil)
	u.settle(t)
	u.focusOn(jobsPanel)
	u.filter()
	u.settle(t)
	p, _ := u.g.View(popupView)
	p.TextArea.TypeString("run")
	u.accept(p)
	if u.m.filter != "run" || len(u.m.visibleJobs()) != 1 {
		t.Errorf("filter = %q", u.m.filter)
	}
	u.enter()
	u.settle(t)
	main, _ := u.g.View(mainView)
	for _, want := range []string{"100_2  FAILED", "line two"} {
		if !strings.Contains(main.Buffer(), want) {
			t.Errorf("main view lacks %q:\n%s", want, main.Buffer())
		}
	}
}

func TestHostListFallsBackToTheFirstHost(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, configFile), "[default]\npush = [\"src\"]\n[hosts.b]\ntarget = \"/x\"\n[hosts.a]\ntarget = \"/y\"\n")
	t.Setenv("CLUSTER", "")
	names, host, err := hostList(root, "")
	if err != nil || strings.Join(names, " ") != "a b" || host != "a" {
		t.Errorf("hostList = %v, %q, %v", names, host, err)
	}
	if _, host, _ = hostList(root, "b"); host != "b" {
		t.Errorf("host = %q, want b", host)
	}
}

func TestWrapLogKeepsEveryRuneAndColorsErrors(t *testing.T) {
	got := wrapLog([]string{"error: abcdefgh", "plain"}, 5)
	if len(got) != 4 {
		t.Fatalf("wrapped = %q, want 4 rows", got)
	}
	if got[0] != paint(red, "error") || got[1] != paint(red, ": abc") || got[2] != paint(red, "defgh") || got[3] != "plain" {
		t.Errorf("wrapped = %q", got)
	}
}

func TestOptionsShowTheRunningTask(t *testing.T) {
	u := newTestTUI(t, scriptRepo(t, ""))
	u.m.busy, u.m.task = true, "push"
	if o := plain(u.options(120)); !strings.Contains(o, "Pushing ") || strings.Contains(o, "Cancel: c") {
		t.Errorf("options = %q", o)
	}
}

func TestOptionsFollowTheFocusAndColonOpensTheCommandPopup(t *testing.T) {
	root := scriptRepo(t, "")
	u := newTestTUI(t, root)
	u.focusOn(snapsPanel)
	if o := u.options(120); !strings.Contains(o, "Drop: d") || strings.Contains(o, "Cancel: c") {
		t.Errorf("snapshots options = %q", o)
	}
	u.focusOn(jobsPanel)
	if o := u.options(120); !strings.Contains(o, "Cancel: c") || !strings.Contains(o, "Cmd: :") {
		t.Errorf("jobs options = %q", o)
	}
	u.exec()
	if u.pop == nil || u.pop.kind != inputPopup || u.pop.title != "Command in work/" {
		t.Errorf("popup = %v", u.pop)
	}
}
