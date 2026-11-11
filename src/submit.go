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

type confirmError string

func (e confirmError) Error() string { return string(e) }

func confirm(yes bool, what string) error {
	if yes {
		return nil
	}
	return confirmError(what + ": nothing done, pass --yes to confirm")
}

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
	Host   string `json:"host"`
	Job    string `json:"job"`
	Commit string `json:"commit"`
}

func runSubmit(w io.Writer, root, hostFlag string, yes, asJSON bool, extra, note string, args []string) error {
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
	if err := confirm(yes, fmt.Sprintf("would submit %s on %s", script, cfg.Host)); err != nil {
		return err
	}
	s, err := syncUp(os.Stderr, root, cfg, true)
	if err != nil {
		return fmt.Errorf("submit to %s: %w", cfg.Host, err)
	}
	job, err := sbatch(cfg, s.Dir+"/"+s.Commit, s.Commit, script, args[1:], extra)
	if err != nil {
		return fmt.Errorf("submit to %s: %w", cfg.Host, err)
	}
	err = appendJob(root, Job{
		Time: time.Now().Format(time.RFC3339), Host: cfg.Host, Job: job, Script: script,
		Commit: s.Commit, Args: args[1:], Sbatch: extra, Note: note,
	})
	if err != nil {
		return fmt.Errorf("job %s submitted on %s but not journaled: %w", job, cfg.Host, err)
	}
	if asJSON {
		return json.NewEncoder(w).Encode(submitRow{cfg.Host, job, s.Commit})
	}
	_, err = fmt.Fprintf(w, "job %s submitted on %s\n", job, cfg.Host)
	return err
}
