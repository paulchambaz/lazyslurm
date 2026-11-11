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
	Host    string `json:"host"`
	Job     string `json:"job,omitempty"`
	Paths   int    `json:"paths"`
	Dropped int    `json:"dropped"`
}

// jobEnv names the variable that tells the pull hooks which job the user pulled for.
const jobEnv = "LAZYSLURM_JOB"

func runPull(w io.Writer, root, hostFlag string, asJSON bool, args []string) error {
	if len(args) > 1 {
		return usageError("pull takes at most one job")
	}
	cfg, err := loadConfig(root, hostFlag)
	if err != nil {
		return err
	}
	job := ""
	if len(args) == 1 {
		jobs, err := readJournal(root)
		if err != nil {
			return err
		}
		j, found, err := findJob(jobs, cfg.Host, args[0])
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("job %s is not in the journal of %s", args[0], cfg.Host)
		}
		job = j.Job
	}
	n, dropped, err := pullFrom(hookOut, root, cfg, job)
	if err != nil {
		return fmt.Errorf("pull from %s: %w", cfg.Host, err)
	}
	if asJSON {
		return json.NewEncoder(w).Encode(pullRow{cfg.Host, job, n, dropped})
	}
	line := fmt.Sprintf("pulled %d paths from %s", n, cfg.Host)
	if job != "" {
		line += " for job " + job
	}
	if dropped > 0 {
		line += fmt.Sprintf(", dropped %d snapshots", dropped)
	}
	_, err = fmt.Fprintln(w, line)
	return err
}

// pullFrom judges the snapshots, runs pre_pull, fetches paths and logs, runs post_pull, then drops the final snapshots.
// A job id is only given to the hooks, in LAZYSLURM_JOB; everything is pulled all the same.
func pullFrom(hooks io.Writer, root string, cfg Config, job string) (paths, dropped int, err error) {
	commit, err := headCommit(root)
	if err != nil {
		return 0, 0, err
	}
	isDirty, err := dirty(root, cfg)
	if err != nil {
		return 0, 0, err
	}
	env := hookEnv(commit, isDirty, cfg.Host)
	if job != "" {
		env = append(env, jobEnv+"="+job)
	}
	dir, err := cfg.Target.resolve()
	if err != nil {
		return 0, 0, err
	}
	infos, err := loadSnapshots(root, cfg, dir)
	if err != nil {
		return 0, 0, err
	}
	if err := targetHook(hooks, "pre_pull", cfg.Target, dir+"/work", cfg.PrePull, env); err != nil {
		return 0, 0, err
	}
	existing, err := existingPaths(cfg, dir)
	if err != nil {
		return 0, 0, err
	}
	if err := pullPaths(root, cfg.Target, dir, existing); err != nil {
		return 0, 0, err
	}
	if err := pullLogs(root, cfg.Target, dir); err != nil {
		return 0, 0, err
	}
	if err := localHook(hooks, "post_pull", root, cfg.PostPull, env); err != nil {
		return 0, 0, err
	}
	var drop []string
	for _, s := range infos {
		switch s.State {
		case stateFinal:
			drop = append(drop, s.Commit)
		case stateStray:
			fmt.Fprintf(hooks, "kept snapshot %s: %d stray files, see snapshots\n", s.Commit[:8], len(s.Stray))
		}
	}
	if err := deleteSnapshots(cfg, dir, drop); err != nil {
		return 0, 0, err
	}
	return len(existing), len(drop), nil
}
