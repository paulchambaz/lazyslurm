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

// fetchStates asks sacct and squeue about jobs and returns their states by job id.
func fetchStates(t Target, jobs []Job, now time.Time) (map[string]jobState, error) {
	if len(jobs) == 0 {
		return nil, nil
	}
	ids := make([]string, len(jobs))
	for i, j := range jobs {
		ids[i] = j.Job
	}
	out, err := t.run("sacct -j "+quote(strings.Join(ids, ","))+" -X -n -P --format=JobID,State", nil)
	if err != nil {
		return nil, fmt.Errorf("sacct: %w", err)
	}
	rows := parseSacct(string(out))
	out, err = t.run(`squeue -h -u "$(id -un)" -o '%i|%Z'`, nil)
	if err != nil {
		return nil, fmt.Errorf("squeue: %w", err)
	}
	return judge(jobs, rows, parseSqueue(string(out)), now), nil
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
