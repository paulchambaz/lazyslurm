package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
)

func short(commit string) string {
	return commit[:min(8, len(commit))]
}

func failedTasks(rows []taskRow) (array string, failed bool) {
	var nums []int
	for _, r := range rows {
		if !finalStates[r.State] || r.State == "COMPLETED" {
			continue
		}
		failed = true
		if _, n, ok := strings.Cut(r.ID, "_"); ok {
			if k, err := strconv.Atoi(n); err == nil {
				nums = append(nums, k)
			}
		}
	}
	slices.Sort(nums)
	list := make([]string, len(nums))
	for i, k := range nums {
		list[i] = strconv.Itoa(k)
	}
	return strings.Join(list, ","), failed
}

func fullCommit(root, commit string) (string, error) {
	c := command("git", "rev-parse", "--verify", commit+"^{commit}")
	c.Dir = root
	out, err := c.Output()
	if err != nil {
		return "", fmt.Errorf("commit %s is not in the local repo", commit)
	}
	return strings.TrimSpace(string(out)), nil
}

func presentPaths(files map[string]bool, push []string) []string {
	var paths []string
	for _, p := range push {
		for f := range files {
			if inside(f, p) {
				paths = append(paths, p)
				break
			}
		}
	}
	return paths
}

func exportCommit(root string, cfg Config, commit, script, tmp string) error {
	files := treeFiles(root, cfg, commit)
	if !files[script] {
		return fmt.Errorf("commit %s no longer holds %s", short(commit), script)
	}
	args := []string{"archive", "--format=tar", commit, "--"}
	archive := command("git", append(args, presentPaths(files, cfg.Push)...)...)
	archive.Dir = root
	untar := command("tar", "-x", "-C", tmp)
	pipe, err := archive.StdoutPipe()
	if err != nil {
		return fmt.Errorf("git archive: %w", err)
	}
	var archiveErr, untarErr bytes.Buffer
	archive.Stderr, untar.Stdin, untar.Stderr = &archiveErr, pipe, &untarErr
	if err := untar.Start(); err != nil {
		return fmt.Errorf("tar: %w", err)
	}
	archiveRun := archive.Run()
	untarRun := untar.Wait()
	if archiveRun != nil {
		return fmt.Errorf("git archive: %w: %s", archiveRun, strings.TrimSpace(archiveErr.String()))
	}
	if untarRun != nil {
		return fmt.Errorf("tar: %w: %s", untarRun, strings.TrimSpace(untarErr.String()))
	}
	return nil
}

func hasSnapshot(t Target, path string) (bool, error) {
	out, err := t.run("if [ -d "+quote(path)+" ]; then echo yes; fi", nil)
	return len(out) > 0, err
}

func rebuild(cfg Config, dir, commit, local string) error {
	snap, tmp := dir+"/"+commit, dir+"/"+commit+".tmp"
	if ok, err := hasSnapshot(cfg.Target, snap); ok || err != nil {
		return err
	}
	if _, err := cfg.Target.run("rm -rf "+quote(tmp), nil); err != nil {
		return err
	}
	args := []string{"-rl", "--checksum", "--link-dest=" + dir + "/work/", "--exclude=.git"}
	for _, p := range cfg.Exclude {
		args = append(args, "--exclude="+p)
	}
	if err := rsync(nil, append(args, local+"/", cfg.Target.path(tmp+"/"))...); err != nil {
		return err
	}
	_, err := cfg.Target.run(prepareScript(cfg, dir, commit+".tmp")+"mv "+quote(tmp)+" "+quote(snap)+"\n", nil)
	return err
}

// ensureSnapshot rebuilds the snapshot of commit from the local repo when the target lost it.
func ensureSnapshot(root string, cfg Config, dir, commit, script string) error {
	if ok, err := hasSnapshot(cfg.Target, dir+"/"+commit); ok || err != nil {
		return err
	}
	local, err := os.MkdirTemp("", "lazyslurm-")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(local)
	if err := exportCommit(root, cfg, commit, script, local); err != nil {
		return err
	}
	unlock, err := cfg.Target.lock(dir)
	if err != nil {
		return err
	}
	err = rebuild(cfg, dir, commit, local)
	if unlockErr := unlock(); err == nil {
		err = unlockErr
	}
	if err != nil {
		return fmt.Errorf("rebuild snapshot %s: %w", short(commit), err)
	}
	return nil
}

func runRetry(w io.Writer, root, hostFlag string, ask confirmer, asJSON bool, args []string) error {
	if len(args) > 1 {
		return usageError("retry takes at most one job")
	}
	jobs, err := readJournal(root)
	if err != nil {
		return err
	}
	var orig Job
	if len(args) == 1 {
		j, found, err := findJob(jobs, hostFlag, args[0])
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("job %s is not in the journal", args[0])
		}
		orig = j
	} else {
		selected, err := loadConfig(root, hostFlag)
		if err != nil {
			return err
		}
		for _, j := range slices.Backward(jobs) {
			if j.Host == selected.Host {
				orig = j
				break
			}
		}
		if orig.Job == "" {
			return fmt.Errorf("no job in the journal for host %s", selected.Host)
		}
	}
	cfg, err := loadConfig(root, orig.Host)
	if err != nil {
		return err
	}
	rows, err := taskRows(cfg.Target, orig.Job)
	if err != nil {
		return fmt.Errorf("retry on %s: %w", cfg.Host, err)
	}
	if len(rows) == 0 {
		return fmt.Errorf("sacct knows no task of job %s on %s", orig.Job, cfg.Host)
	}
	array, failed := failedTasks(rows)
	row := submitRow{Host: cfg.Host, Commit: orig.Commit, RetryOf: orig.Job, Array: array}
	if !failed {
		if asJSON {
			return json.NewEncoder(w).Encode(row)
		}
		_, err = fmt.Fprintf(w, "nothing to retry for job %s\n", orig.Job)
		return err
	}
	what := fmt.Sprintf("would retry job %s on %s", orig.Job, cfg.Host)
	if array != "" {
		what += " (tasks " + array + ")"
	}
	if err := ask.ask(what); err != nil {
		return err
	}
	commit, err := fullCommit(root, orig.Commit)
	if err != nil {
		return err
	}
	dir, err := cfg.Target.resolve()
	if err != nil {
		return err
	}
	if err := ensureSnapshot(root, cfg, dir, commit, orig.Script); err != nil {
		return fmt.Errorf("retry on %s: %w", cfg.Host, err)
	}
	snap := dir + "/" + commit
	if err := targetHook(os.Stderr, "post_push", cfg.Target, snap, cfg.PostPush, hookEnv(commit, false)); err != nil {
		return err
	}
	extra := orig.Sbatch
	if array != "" {
		extra = strings.TrimSpace(extra + " --array=" + array)
	}
	job, err := launch(root, cfg, snap, Job{
		Script: orig.Script, Commit: commit, Args: orig.Args, Sbatch: orig.Sbatch,
		Array: array, RetryOf: orig.Job,
	}, extra)
	if err != nil {
		return fmt.Errorf("retry on %s: %w", cfg.Host, err)
	}
	row.Job, row.Commit = job, commit
	if asJSON {
		return json.NewEncoder(w).Encode(row)
	}
	_, err = fmt.Fprintf(w, "job %s submitted on %s (retry of %s)\n", job, cfg.Host, orig.Job)
	return err
}
