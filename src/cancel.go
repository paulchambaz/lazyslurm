package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type cancelRow struct {
	Host string `json:"host"`
	Job  string `json:"job"`
}

func inSpec(spec string, n int) bool {
	spec = strings.Trim(spec, "[]")
	spec, _, _ = strings.Cut(spec, "%")
	for part := range strings.SplitSeq(spec, ",") {
		lo, hi, isRange := strings.Cut(part, "-")
		a, errA := strconv.Atoi(lo)
		b, errB := strconv.Atoi(hi)
		if !isRange {
			b, errB = a, errA
		}
		if errA == nil && errB == nil && a <= n && n <= b {
			return true
		}
	}
	return false
}

func queued(ids []string, id string) bool {
	base, task, isTask := strings.Cut(id, "_")
	n, err := strconv.Atoi(task)
	for _, q := range ids {
		qBase, spec, qTask := strings.Cut(q, "_")
		switch {
		case q == id, !isTask && qBase == id:
			return true
		case isTask && err == nil && qBase == base && qTask && strings.HasPrefix(spec, "[") && inSpec(spec, n):
			return true
		}
	}
	return false
}

func runCancel(w io.Writer, root, hostFlag string, ask confirmer, asJSON bool, args []string) error {
	if len(args) != 1 {
		return usageError("cancel needs one job")
	}
	jobs, err := readJournal(root)
	if err != nil {
		return err
	}
	j, found, err := findJob(jobs, hostFlag, args[0])
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
	ids, err := activeIDs(cfg.Target)
	if err != nil {
		return fmt.Errorf("cancel on %s: %w", cfg.Host, err)
	}
	if !queued(ids, args[0]) {
		return fmt.Errorf("job %s is not active on %s", args[0], cfg.Host)
	}
	if err := ask.ask(fmt.Sprintf("would cancel job %s on %s", args[0], cfg.Host)); err != nil {
		return err
	}
	if err := scancel(cfg.Target, args[0]); err != nil {
		return fmt.Errorf("cancel on %s: %w", cfg.Host, err)
	}
	if asJSON {
		return json.NewEncoder(w).Encode(cancelRow{cfg.Host, args[0]})
	}
	_, err = fmt.Fprintf(w, "cancelled %s on %s\n", args[0], cfg.Host)
	return err
}
