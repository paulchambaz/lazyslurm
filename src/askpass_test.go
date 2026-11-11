package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == askpassCommand {
		os.Exit(runAskpass(os.Args[2:]))
	}
	os.Exit(m.Run())
}

func TestCredentialTitle(t *testing.T) {
	tests := []struct {
		prompt string
		title  string
		secret bool
	}{
		{"Enter passphrase for key '/home/u/.ssh/id_ed25519': ", "Passphrase", true},
		{"user@host's password: ", "Password", true},
		{"(user@host) Password: ", "Password", true},
		{"Password for 'https://x': ", "Password", true},
		{"Username for 'https://x': ", "Username", false},
		{"Enter PIN for ED25519-SK key /k: ", "PIN", true},
		{"Your 2FA Token: ", "Token", true},
		{"The authenticity of host 'h' can't be established.\nED25519 key fingerprint is SHA256:abc.\nAre you sure you want to continue connecting (yes/no/[fingerprint])? ",
			"Are you sure you want to continue connecting (yes/no/[fingerprint])?", false},
		{"", "", false},
	}
	for _, tt := range tests {
		if title, secret := credentialTitle(tt.prompt); title != tt.title || secret != tt.secret {
			t.Errorf("credentialTitle(%q) = %q, %v, want %q, %v", tt.prompt, title, secret, tt.title, tt.secret)
		}
	}
}

func useAskpass(t *testing.T, ask func(string, <-chan struct{}) (string, bool)) *askpass {
	t.Helper()
	a, err := startAskpass(ask)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.close)
	for _, kv := range a.env() {
		name, value, _ := strings.Cut(kv, "=")
		t.Setenv(name, value)
	}
	return a
}

func TestAskpassRelaysThePromptAndTheAnswer(t *testing.T) {
	var got string
	a := useAskpass(t, func(prompt string, _ <-chan struct{}) (string, bool) {
		got = prompt
		return "pa ss\tword", true
	})
	prompt := "line one\nEnter passphrase for key '/k': "
	out, err := exec.Command(filepath.Join(a.dir, "askpass"), prompt).Output()
	if err != nil || string(out) != "pa ss\tword\n" || got != prompt {
		t.Errorf("answer %q, prompt %q, %v", out, got, err)
	}
}

func TestAskpassFailsWhenTheUserCancelsOrThereIsNoUI(t *testing.T) {
	a := useAskpass(t, func(string, <-chan struct{}) (string, bool) { return "", false })
	out, err := exec.Command(filepath.Join(a.dir, "askpass"), "p").Output()
	if err == nil || len(out) != 0 {
		t.Errorf("cancelled prompt: %q, %v", out, err)
	}
	t.Setenv(askpassEnv, "")
	if _, err := exec.Command(filepath.Join(a.dir, "askpass"), "p").Output(); err == nil {
		t.Error("an askpass without a UI succeeded")
	}
}

func TestAskpassCloseReleasesAWaitingPrompt(t *testing.T) {
	asked := make(chan struct{})
	a, err := startAskpass(func(_ string, stop <-chan struct{}) (string, bool) {
		close(asked)
		<-stop
		return "", false
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(askpassEnv, filepath.Join(a.dir, "sock"))
	done := make(chan error, 1)
	go func() { done <- exec.Command(filepath.Join(a.dir, "askpass"), "p").Run() }()
	<-asked
	a.close()
	select {
	case err := <-done:
		if err == nil {
			t.Error("a released prompt succeeded")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the prompt is still waiting after close")
	}
	if exists(a.dir) {
		t.Error("the socket directory is still there")
	}
}

const askingSSH = `#!/bin/sh
pass=$("$SSH_ASKPASS" "Enter passphrase for key '/k': ") || exit 255
echo "got:$pass"
`

type asked struct {
	out string
	err error
}

func askingTUI(t *testing.T) (*tui, chan func(), chan asked) {
	t.Helper()
	bin := t.TempDir()
	write(t, filepath.Join(bin, "ssh"), askingSSH)
	if err := os.Chmod(filepath.Join(bin, "ssh"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	u := newTestTUI(t, scriptRepo(t, ""))
	u.g.SetManagerFunc(u.layout)
	posted := make(chan func(), 8)
	u.post = func(f func()) { posted <- f }
	useAskpass(t, u.askSecret)
	results := make(chan asked, 1)
	go func() {
		out, err := Target{Alias: "x", Dir: "d"}.run("true", nil)
		results <- asked{string(out), err}
	}()
	return u, posted, results
}

func (u *tui) redraw(t *testing.T) {
	t.Helper()
	if err := u.g.ForceLayoutAndRedraw(); err != nil {
		t.Fatal(err)
	}
}

func (u *tui) showAsk(t *testing.T, posted chan func()) {
	t.Helper()
	select {
	case f := <-posted:
		f()
	case <-time.After(10 * time.Second):
		t.Fatal("ssh never asked")
	}
	u.redraw(t)
}

func TestPasswordPopupAnswersSshAndHidesTheInput(t *testing.T) {
	u, posted, results := askingTUI(t)
	u.showAsk(t, posted)
	if u.pop == nil || u.pop.kind != credentialPopup || u.pop.title != "Passphrase" || !u.pop.secret {
		t.Fatalf("popup = %+v", u.pop)
	}
	v, err := u.g.View(popupView)
	if err != nil {
		t.Fatal(err)
	}
	v.TextArea.TypeString("hunter2")
	v.RenderTextArea()
	u.redraw(t)
	if screen := u.g.Snapshot(); strings.Contains(screen, "hunter2") || !strings.Contains(screen, "*******") || !strings.Contains(screen, "Passphrase") {
		t.Errorf("screen shows the password or not the prompt:\n%s", screen)
	}
	if err := u.accept(v); err != nil {
		t.Fatal(err)
	}
	if r := <-results; r.err != nil || r.out != "got:hunter2\n" {
		t.Errorf("ssh got %q, %v", r.out, r.err)
	}
	if u.pop != nil {
		t.Error("the popup is still open")
	}
}

func TestEscapeCancelsThePasswordPrompt(t *testing.T) {
	u, posted, results := askingTUI(t)
	u.showAsk(t, posted)
	if err := u.dismiss(); err != nil {
		t.Fatal(err)
	}
	if r := <-results; r.err == nil {
		t.Errorf("a cancelled prompt succeeded: %q", r.out)
	}
}

func TestPasswordPromptWaitsForTheOpenPopup(t *testing.T) {
	u, posted, results := askingTUI(t)
	u.open(&popup{kind: filterPopup})
	u.showAsk(t, posted)
	if u.pop.kind != filterPopup || len(u.asks) != 1 {
		t.Fatalf("the prompt replaced the open popup: %+v, %d waiting", u.pop, len(u.asks))
	}
	if err := u.dismiss(); err != nil {
		t.Fatal(err)
	}
	u.redraw(t)
	if u.pop == nil || u.pop.kind != credentialPopup {
		t.Fatalf("popup after the first closed = %+v", u.pop)
	}
	if err := u.dismiss(); err != nil {
		t.Fatal(err)
	}
	<-results
}

func TestUIAnswersPromptsOnlyWhileItRuns(t *testing.T) {
	setupFakeSSH(t)
	u := newTestTUI(t, scriptRepo(t, ""))
	t.Cleanup(func() {
		u.stopListening()
		batchMode = "yes"
	})
	if err := u.listenForPasswords(); err != nil {
		t.Fatal(err)
	}
	if batchMode != "no" || os.Getenv("SSH_ASKPASS_REQUIRE") != "force" || !exists(os.Getenv("SSH_ASKPASS")) {
		t.Errorf("not listening: batch %q, %q, %q", batchMode, os.Getenv("SSH_ASKPASS_REQUIRE"), os.Getenv("SSH_ASKPASS"))
	}
	askpassScript := os.Getenv("SSH_ASKPASS")
	u.stopListening()
	if batchMode != "yes" || os.Getenv("SSH_ASKPASS") != "" || os.Getenv(askpassEnv) != "" || exists(askpassScript) {
		t.Errorf("still listening: batch %q, %q", batchMode, os.Getenv("SSH_ASKPASS"))
	}
}
