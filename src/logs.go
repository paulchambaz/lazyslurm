package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func catRemote(t Target, path string, follow bool, w io.Writer) error {
	read := "cat"
	if follow {
		read = "tail -n +1 -F"
	}
	script := fmt.Sprintf(`[ -f %[1]s ] || { echo 'no such file' >&2; exit 1; }; exec %[2]s -- %[1]s`, quote(path), read)
	if err := t.stream(script, nil, w); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return nil
}

func catLocal(path string, w io.Writer) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("read log: %w", err)
	}
	defer f.Close()
	if _, err := io.Copy(w, f); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return nil
}

func runLogs(w io.Writer, root, hostFlag string, follow, stderr bool, args []string) error {
	if len(args) != 1 {
		return usageError("logs needs one job")
	}
	id := args[0]
	jobs, err := readJournal(root)
	if err != nil {
		return err
	}
	j, found, err := findJob(jobs, hostFlag, id)
	if err != nil {
		return err
	}
	if found {
		hostFlag = j.Host
	}
	cfg, err := loadConfig(root, hostFlag)
	if err != nil {
		return err
	}
	ext := ".out"
	if stderr {
		ext = ".err"
	}
	var tried []error
	stdoutPath, stderrPath, err := jobLogPaths(cfg.Target, id)
	if err == nil {
		path := stdoutPath
		if stderr && stderrPath != "" {
			path = stderrPath
		}
		if err = catRemote(cfg.Target, path, follow, w); err == nil {
			return nil
		}
	}
	tried = append(tried, err)
	if found {
		dir, err := cfg.Target.resolve()
		if err != nil {
			return err
		}
		name := "slurm-" + id + ext
		err = catRemote(cfg.Target, dir+"/"+j.Commit+"/"+name, follow, w)
		if err == nil {
			return nil
		}
		tried = append(tried, err)
		err = catLocal(filepath.Join(stateDir(root), "logs", j.Commit, name), w)
		if err == nil {
			return nil
		}
		tried = append(tried, err)
	}
	return fmt.Errorf("no log found for job %s: %w", id, errors.Join(tried...))
}
