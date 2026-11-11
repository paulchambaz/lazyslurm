package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const journalVersion = 3

// Job is one line of the journal: one sbatch submitted by lazyslurm.
type Job struct {
	V       int      `json:"v"`
	Time    string   `json:"time"`
	Host    string   `json:"host"`
	Job     string   `json:"job"`
	Script  string   `json:"script"`
	Commit  string   `json:"commit"`
	Args    []string `json:"args,omitempty"`
	Sbatch  string   `json:"sbatch,omitempty"`
	Note    string   `json:"note,omitempty"`
	Array   string   `json:"array,omitempty"`
	RetryOf string   `json:"retry_of,omitempty"`
}

type seenSet map[string]bool

func (s seenSet) has(j Job) bool {
	return s[j.Host+" "+j.Job] || s[j.Job]
}

// findJob returns the newest journal line of a job, on host when given.
func findJob(jobs []Job, host, id string) (Job, bool, error) {
	var found Job
	ok := false
	for _, j := range slices.Backward(jobs) {
		if j.Job != baseJob(id) || host != "" && j.Host != host {
			continue
		}
		if ok && j.Host != found.Host {
			return Job{}, false, fmt.Errorf("job %s exists on %s and %s: pass -H", id, found.Host, j.Host)
		}
		if !ok {
			found, ok = j, true
		}
	}
	return found, ok, nil
}

func journalPath(root string) string {
	return filepath.Join(stateDir(root), "jobs.jsonl")
}

func seenPath(root string) string {
	return filepath.Join(stateDir(root), "seen")
}

func appendLine(root, path string, line []byte) error {
	if err := ensureStateDir(root); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	return f.Close()
}

func appendJob(root string, j Job) error {
	j.V = journalVersion
	line, err := json.Marshal(j)
	if err != nil {
		return fmt.Errorf("encode job %s: %w", j.Job, err)
	}
	return appendLine(root, journalPath(root), line)
}

func addSeen(root, host, job string) error {
	return appendLine(root, seenPath(root), []byte(host+" "+job))
}

// readState reads a state file, importing its old outputs/ copy once if it is missing.
func readState(root, path, old string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		return data, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	data, err = os.ReadFile(filepath.Join(root, old))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", old, err)
	}
	if err := ensureStateDir(root); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	return data, nil
}

func readJournal(root string) ([]Job, error) {
	data, err := readState(root, journalPath(root), "outputs/slurms.jsonl")
	if err != nil {
		return nil, err
	}
	var jobs []Job
	for i, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var j Job
		if err := json.Unmarshal([]byte(line), &j); err != nil {
			return nil, fmt.Errorf("jobs.jsonl line %d: %w", i+1, err)
		}
		if j.V < 1 || j.V > journalVersion {
			return nil, fmt.Errorf("jobs.jsonl line %d: unsupported version %d", i+1, j.V)
		}
		jobs = append(jobs, j)
	}
	return jobs, nil
}

func readSeen(root string) (seenSet, error) {
	data, err := readState(root, seenPath(root), "outputs/slurms.seen")
	if err != nil {
		return nil, err
	}
	seen := seenSet{}
	for line := range strings.SplitSeq(string(data), "\n") {
		if fields := strings.Fields(line); len(fields) > 0 {
			seen[strings.Join(fields, " ")] = true
		}
	}
	return seen, nil
}
