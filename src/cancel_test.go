package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func cancel(root string, ask confirmer, args ...string) (string, error) {
	var out bytes.Buffer
	err := runCancel(&out, root, "", ask, false, args)
	return out.String(), err
}

func TestCancelNeedsAnActiveJobAndAConfirmation(t *testing.T) {
	for name, tg := range testTargets(t) {
		fake := setupFakeSlurm(t)
		root := jobRepo(t, tg, "")
		if _, err := submit(root, "slurm/run.sh"); err != nil {
			t.Fatal(err)
		}
		if _, err := cancel(root, confirmer{Yes: true}, "100"); err == nil || !strings.Contains(err.Error(), "not active") {
			t.Errorf("%s: inactive job = %v", name, err)
		}
		write(t, filepath.Join(fake, "squeue"), "100|/x\n")
		if _, err := cancel(root, confirmer{}, "100"); err == nil || !strings.Contains(err.Error(), "--yes") {
			t.Errorf("%s: unconfirmed cancel = %v", name, err)
		}
		declined := confirmer{Tty: true, In: strings.NewReader("\n"), Out: &bytes.Buffer{}}
		if _, err := cancel(root, declined, "100"); err == nil || exists(filepath.Join(fake, "scancel.log")) {
			t.Errorf("%s: declined cancel = %v", name, err)
		}
		out, err := cancel(root, confirmer{Yes: true}, "100")
		if err != nil || out != "cancelled 100 on h\n" {
			t.Errorf("%s: cancel = %q, %v", name, out, err)
		}
		if log, _ := os.ReadFile(filepath.Join(fake, "scancel.log")); string(log) != "100\n" {
			t.Errorf("%s: scancel saw %q", name, log)
		}
	}
}

func TestCancelTasksAndJobsOutsideTheJournal(t *testing.T) {
	for name, tg := range testTargets(t) {
		fake := setupFakeSlurm(t)
		root := jobRepo(t, tg, "")
		write(t, filepath.Join(fake, "squeue"), "7_2|/x\n7_[4-9]|/x\n8|/y\n")
		for id, ok := range map[string]bool{"7_2": true, "7_5": true, "7_3": false, "7": true, "8": true, "9": false} {
			_, err := cancel(root, confirmer{Yes: true}, id)
			if (err == nil) != ok {
				t.Errorf("%s: cancel %s = %v, want ok %v", name, id, err, ok)
			}
		}
		if _, err := cancel(root, confirmer{Yes: true}); err == nil {
			t.Errorf("%s: no job must be a usage error", name)
		}
	}
}
