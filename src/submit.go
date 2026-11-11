package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

func checkScript(root string, cfg Config, script string) error {
	if badPath(script) || !insideAny(script, cfg.Push) {
		return fmt.Errorf("%s is not inside the push paths", script)
	}
	c := command("git", "ls-files", "--error-unmatch", "--", script)
	c.Dir = root
	if err := c.Run(); err != nil {
		return fmt.Errorf("%s is not tracked by git", script)
	}
	isDirty, err := dirty(root, cfg)
	if err != nil {
		return err
	}
	if isDirty {
		return errors.New("the tree under push is not clean: commit or stash first")
	}
	return nil
}

type submitRow struct {
	Host    string `json:"host"`
	Job     string `json:"job"`
	Commit  string `json:"commit"`
	RetryOf string `json:"retry_of,omitempty"`
	Array   string `json:"array,omitempty"`
}

// launch runs sbatch for j in snap with extra options and journals the new job.
func launch(root string, cfg Config, snap string, j Job, extra string) (string, error) {
	job, err := sbatch(cfg, snap, j.Commit, j.Script, j.Args, extra)
	if err != nil {
		return "", err
	}
	j.Time, j.Host, j.Job = time.Now().Format(time.RFC3339), cfg.Host, job
	if err := appendJob(root, j); err != nil {
		return "", fmt.Errorf("job %s submitted on %s but not journaled: %w", job, cfg.Host, err)
	}
	return job, nil
}

func runSubmit(w io.Writer, root, hostFlag string, ask confirmer, asJSON bool, extra, note string, args []string) error {
	if len(args) == 0 {
		return usageError("submit needs a script")
	}
	cfg, err := loadConfig(root, hostFlag)
	if err != nil {
		return err
	}
	script := filepath.Clean(args[0])
	if err := checkScript(root, cfg, script); err != nil {
		return err
	}
	if err := ask.ask(fmt.Sprintf("would submit %s on %s", script, cfg.Host)); err != nil {
		return err
	}
	s, err := syncUp(os.Stderr, root, cfg, true)
	if err != nil {
		return fmt.Errorf("submit to %s: %w", cfg.Host, err)
	}
	job, err := launch(root, cfg, s.Dir+"/"+s.Commit, Job{
		Script: script, Commit: s.Commit, Args: args[1:], Sbatch: extra, Note: note,
	}, extra)
	if err != nil {
		return fmt.Errorf("submit to %s: %w", cfg.Host, err)
	}
	if asJSON {
		return json.NewEncoder(w).Encode(submitRow{Host: cfg.Host, Job: job, Commit: s.Commit})
	}
	_, err = fmt.Fprintf(w, "job %s submitted on %s\n", job, cfg.Host)
	return err
}
