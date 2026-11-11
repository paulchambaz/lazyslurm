package main

import (
	"fmt"
	"maps"
	"slices"
	"time"
)

const firstQuery = 100

// refresh reads the journal and asks the host for states and snapshots, keeping prev where a step fails.
func refresh(root string, cfg Config, prev fresh) (fresh, error) {
	f := fresh{jobs: prev.jobs, states: prev.states, snaps: prev.snaps}
	journal, err := readJournal(root)
	if err != nil {
		return f, err
	}
	f.jobs = nil
	for _, j := range slices.Backward(journal) {
		if j.Host == cfg.Host {
			f.jobs = append(f.jobs, j)
		}
	}
	var query []Job
	for _, j := range f.jobs {
		if st, ok := prev.states[j.Job]; !ok || !st.Final {
			query = append(query, j)
		}
	}
	query = query[:min(len(query), firstQuery)]
	f.states = maps.Clone(prev.states)
	if f.states == nil {
		f.states = map[string]jobState{}
	}
	if len(query) > 0 {
		states, _, err := fetchStates(cfg.Target, query, time.Now())
		if err != nil {
			return f, fmt.Errorf("states on %s: %w", cfg.Host, err)
		}
		maps.Copy(f.states, states)
	}
	seen, err := readSeen(root)
	if err != nil {
		return f, err
	}
	for _, j := range query {
		if st := f.states[j.Job]; st.Final && !seen.has(j) {
			if err := addSeen(root, j.Host, j.Job); err != nil {
				return f, err
			}
		}
	}
	dir, err := cfg.Target.resolve()
	if err != nil {
		return f, err
	}
	f.snaps, err = loadSnapshots(root, cfg, dir)
	if err != nil {
		f.snaps = prev.snaps
		return f, fmt.Errorf("snapshots on %s: %w", cfg.Host, err)
	}
	return f, nil
}
