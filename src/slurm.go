package main

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var finalStates = map[string]bool{
	"COMPLETED": true, "FAILED": true, "TIMEOUT": true, "CANCELLED": true,
	"NODE_FAIL": true, "OUT_OF_MEMORY": true, "BOOT_FAIL": true, "DEADLINE": true,
	"PREEMPTED": true, "REVOKED": true,
}

const accountingDelay = time.Hour

type jobState struct {
	States map[string]int
	Final  bool
}

func baseJob(id string) string {
	base, _, _ := strings.Cut(id, "_")
	return base
}

func parseSacct(out string) map[string][]string {
	rows := map[string][]string{}
	for line := range strings.SplitSeq(out, "\n") {
		id, state, ok := strings.Cut(line, "|")
		fields := strings.Fields(state)
		if !ok || len(fields) == 0 {
			continue
		}
		rows[baseJob(id)] = append(rows[baseJob(id)], fields[0])
	}
	return rows
}

func parseSqueue(out string) map[string][]string {
	queued := map[string][]string{}
	for line := range strings.SplitSeq(out, "\n") {
		id, dir, ok := strings.Cut(line, "|")
		if ok {
			queued[baseJob(id)] = append(queued[baseJob(id)], dir)
		}
	}
	return queued
}

func older(stamp string, now time.Time) bool {
	t, err := time.Parse(time.RFC3339, stamp)
	return err == nil && now.Sub(t) > accountingDelay
}

func judge(jobs []Job, rows, queued map[string][]string, now time.Time) map[string]jobState {
	states := make(map[string]jobState, len(jobs))
	for _, j := range jobs {
		s := jobState{States: map[string]int{}, Final: true}
		for _, state := range rows[j.Job] {
			s.States[state]++
			if !finalStates[state] {
				s.Final = false
			}
		}
		if len(rows[j.Job]) == 0 {
			_, running := queued[j.Job]
			s.Final = !running && older(j.Time, now)
		}
		states[j.Job] = s
	}
	return states
}

// fetchStates asks sacct and squeue about jobs and returns their states by job id and the working directories in the queue.
func fetchStates(t Target, jobs []Job, now time.Time) (map[string]jobState, []string, error) {
	rows := map[string][]string{}
	if len(jobs) > 0 {
		ids := make([]string, len(jobs))
		for i, j := range jobs {
			ids[i] = j.Job
		}
		out, err := t.run("sacct -j "+quote(strings.Join(ids, ","))+" -X -n -P --format=JobID,State", nil)
		if err != nil {
			return nil, nil, fmt.Errorf("sacct: %w", err)
		}
		rows = parseSacct(string(out))
	}
	out, err := t.run(`squeue -h -u "$(id -un)" -o '%i|%Z'`, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("squeue: %w", err)
	}
	queued := parseSqueue(string(out))
	var dirs []string
	for _, d := range queued {
		dirs = append(dirs, d...)
	}
	return judge(jobs, rows, queued, now), dirs, nil
}

type taskRow struct {
	ID    string
	State string
}

func parseTasks(out string) []taskRow {
	var tasks []taskRow
	for line := range strings.SplitSeq(out, "\n") {
		id, state, ok := strings.Cut(line, "|")
		fields := strings.Fields(state)
		if ok && len(fields) > 0 {
			tasks = append(tasks, taskRow{id, fields[0]})
		}
	}
	return tasks
}

// taskRows lists the tasks of a job with their states, as sacct knows them.
func taskRows(t Target, job string) ([]taskRow, error) {
	out, err := t.run("sacct -j "+quote(job)+" -X -n -P --format=JobID,State", nil)
	if err != nil {
		return nil, fmt.Errorf("sacct: %w", err)
	}
	return parseTasks(string(out)), nil
}

// activeIDs lists the ids of the user's jobs in the queue.
func activeIDs(t Target) ([]string, error) {
	out, err := t.run(`squeue -h -u "$(id -un)" -o %i`, nil)
	if err != nil {
		return nil, fmt.Errorf("squeue: %w", err)
	}
	var ids []string
	for line := range strings.SplitSeq(string(out), "\n") {
		id, _, _ := strings.Cut(strings.TrimSpace(line), "|")
		if id != "" {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func scancel(t Target, id string) error {
	if _, err := t.run("scancel "+quote(id), nil); err != nil {
		return fmt.Errorf("scancel: %w", err)
	}
	return nil
}

// jobLogPaths asks scontrol where Slurm writes the stdout and stderr of a job.
func jobLogPaths(t Target, id string) (stdout, stderr string, err error) {
	out, err := t.run("scontrol show job "+quote(id), nil)
	if err != nil {
		return "", "", fmt.Errorf("scontrol: %w", err)
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		line = strings.TrimSpace(line)
		if p, ok := strings.CutPrefix(line, "StdOut="); ok && stdout == "" {
			stdout = p
		}
		if p, ok := strings.CutPrefix(line, "StdErr="); ok && stderr == "" {
			stderr = p
		}
	}
	if stdout == "" {
		return "", "", errors.New("scontrol: no StdOut for job " + id)
	}
	return stdout, stderr, nil
}

// sbatch submits script from snap on the target and returns the job id.
func sbatch(cfg Config, snap, commit, script string, args []string, extra string) (string, error) {
	line := "cd " + quote(snap) + " && sbatch --parsable --export=" + quote("ALL,GIT_COMMIT="+commit)
	for _, opts := range []string{cfg.Sbatch, extra} {
		if opts != "" {
			line += " " + opts
		}
	}
	line += " " + quote(script) + " " + quoteAll(args)
	out, err := cfg.Target.run(line, nil)
	if err != nil {
		return "", fmt.Errorf("sbatch: %w", err)
	}
	id, _, _ := strings.Cut(strings.TrimSpace(string(out)), ";")
	if id == "" {
		return "", errors.New("sbatch printed no job id")
	}
	return id, nil
}
