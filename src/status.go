package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
)

type statusRow struct {
	Host   string `json:"host"`
	Job    string `json:"job"`
	Time   string `json:"time"`
	Script string `json:"script"`
	Commit string `json:"commit"`
	Note   string `json:"note"`
}

func runStatus(w io.Writer, root, hostFlag string, all, asJSON bool, args []string) error {
	if len(args) > 1 {
		return usageError("status takes at most one job")
	}
	host := ""
	cfg, err := loadConfig(root, hostFlag)
	switch {
	case err == nil:
		host = cfg.Host
	case !errors.Is(err, errNoHost):
		return err
	}
	jobs, err := readJournal(root)
	if err != nil {
		return err
	}
	seen, err := readSeen(root)
	if err != nil {
		return err
	}
	slices.Reverse(jobs)
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
		if err := printRow(w, j, host == "", asJSON); err != nil {
			return err
		}
	}
	return nil
}

func printRow(w io.Writer, j Job, withHost, asJSON bool) error {
	if asJSON {
		return json.NewEncoder(w).Encode(statusRow{j.Host, j.Job, j.Time, j.Script, j.Commit, j.Note})
	}
	commit := j.Commit
	if len(commit) > 8 {
		commit = commit[:8]
	}
	line := fmt.Sprintf("%s  %s  %s  %s", j.Job, j.Time, j.Script, commit)
	if withHost {
		line = j.Host + "  " + line
	}
	if j.Note != "" {
		line += "  " + j.Note
	}
	_, err := fmt.Fprintln(w, line)
	return err
}
