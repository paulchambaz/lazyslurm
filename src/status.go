package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
)

type statusRow struct {
	Host   string `json:"host"`
	Job    string `json:"job"`
	Time   string `json:"time"`
	Script string `json:"script"`
	Commit string `json:"commit"`
	Note   string `json:"note"`

	States map[string]int `json:"states,omitempty"`
	Final  *bool          `json:"final,omitempty"`
}

func runStatus(w io.Writer, root, hostFlag string, all, asJSON bool, args []string) error {
	if len(args) > 1 {
		return usageError("status takes at most one job")
	}
	cfg, err := loadConfig(root, hostFlag)
	if err != nil && !errors.Is(err, errNoHost) {
		return err
	}
	host := cfg.Host
	jobs, err := readJournal(root)
	if err != nil {
		return err
	}
	seen, err := readSeen(root)
	if err != nil {
		return err
	}
	slices.Reverse(jobs)
	var shown []Job
	for _, j := range jobs {
		if host != "" && j.Host != host {
			continue
		}
		if len(args) == 1 && j.Job != args[0] {
			continue
		}
		if len(args) == 0 && !all && seen.has(j) {
			continue
		}
		shown = append(shown, j)
	}
	var states map[string]jobState
	if host != "" && len(shown) > 0 {
		states, _, err = fetchStates(cfg.Target, shown, time.Now())
		if err != nil {
			return fmt.Errorf("states on %s: %w", host, err)
		}
	}
	for _, j := range shown {
		var st *jobState
		if s, ok := states[j.Job]; ok {
			st = &s
		}
		if err := printRow(w, j, host == "", asJSON, st); err != nil {
			return err
		}
		if st != nil && st.Final && !seen.has(j) {
			if err := addSeen(root, j.Host, j.Job); err != nil {
				return err
			}
		}
	}
	return nil
}

func countsText(states map[string]int) string {
	names := make([]string, 0, len(states))
	for name := range states {
		names = append(names, name)
	}
	slices.Sort(names)
	parts := make([]string, len(names))
	for i, name := range names {
		parts[i] = fmt.Sprintf("%d %s", states[name], name)
	}
	return strings.Join(parts, "  ")
}

func printRow(w io.Writer, j Job, withHost, asJSON bool, st *jobState) error {
	if asJSON {
		row := statusRow{Host: j.Host, Job: j.Job, Time: j.Time, Script: j.Script, Commit: j.Commit, Note: j.Note}
		if st != nil {
			row.States, row.Final = st.States, &st.Final
		}
		return json.NewEncoder(w).Encode(row)
	}
	commit := j.Commit
	if len(commit) > 8 {
		commit = commit[:8]
	}
	line := fmt.Sprintf("%s  %s  %s  %s", j.Job, j.Time, j.Script, commit)
	if withHost {
		line = j.Host + "  " + line
	}
	if st != nil && len(st.States) > 0 {
		line += "  " + countsText(st.States)
	}
	if j.Note != "" {
		line += "  " + j.Note
	}
	_, err := fmt.Fprintln(w, line)
	return err
}
