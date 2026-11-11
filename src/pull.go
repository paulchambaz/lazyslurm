package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func existingPaths(cfg Config, dir string) ([]string, error) {
	if len(cfg.Pull) == 0 {
		return nil, nil
	}
	out, err := cfg.Target.run(fmt.Sprintf(
		`cd %s && for p in %s; do if [ -e "$p" ]; then printf '%%s\0' "$p"; fi; done`,
		quote(dir+"/keep"), quoteAll(cfg.Pull)), nil)
	if err != nil {
		return nil, fmt.Errorf("list keep/: %w", err)
	}
	var paths []string
	for p := range strings.SplitSeq(string(out), "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

func pullPaths(root string, t Target, dir string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	args := []string{"-aR"}
	for _, p := range paths {
		args = append(args, t.path(dir+"/keep/./"+p))
	}
	return rsync(nil, append(args, root+"/")...)
}

func pullLogs(root string, t Target, dir string) error {
	logs := filepath.Join(stateDir(root), "logs")
	if err := ensureStateDir(root); err != nil {
		return err
	}
	if err := os.MkdirAll(logs, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", logs, err)
	}
	return rsync(nil, "-rtm", "--exclude=/work/", "--exclude=/keep/", "--include=/*/",
		"--include=/*/slurm-*.out", "--include=/*/slurm-*.err", "--exclude=*",
		t.path(dir+"/"), logs+"/")
}

type pullRow struct {
	Host  string `json:"host"`
	Paths int    `json:"paths"`
}

func runPull(w io.Writer, root, hostFlag string, asJSON bool, args []string) error {
	if len(args) > 0 {
		return usageError("pull takes no arguments")
	}
	cfg, err := loadConfig(root, hostFlag)
	if err != nil {
		return err
	}
	n, err := pullFrom(os.Stderr, root, cfg)
	if err != nil {
		return fmt.Errorf("pull from %s: %w", cfg.Host, err)
	}
	if asJSON {
		return json.NewEncoder(w).Encode(pullRow{cfg.Host, n})
	}
	_, err = fmt.Fprintf(w, "pulled %d paths from %s\n", n, cfg.Host)
	return err
}

// pullFrom runs pre_pull, fetches the pull paths and snapshot logs, then runs post_pull.
func pullFrom(hooks io.Writer, root string, cfg Config) (int, error) {
	commit, err := headCommit(root)
	if err != nil {
		return 0, err
	}
	isDirty, err := dirty(root, cfg)
	if err != nil {
		return 0, err
	}
	env := hookEnv(commit, isDirty)
	dir, err := cfg.Target.resolve()
	if err != nil {
		return 0, err
	}
	if err := targetHook(hooks, "pre_pull", cfg.Target, dir+"/work", cfg.PrePull, env); err != nil {
		return 0, err
	}
	paths, err := existingPaths(cfg, dir)
	if err != nil {
		return 0, err
	}
	if err := pullPaths(root, cfg.Target, dir, paths); err != nil {
		return 0, err
	}
	if err := pullLogs(root, cfg.Target, dir); err != nil {
		return 0, err
	}
	return len(paths), localHook(hooks, "post_pull", root, cfg.PostPull, env)
}
