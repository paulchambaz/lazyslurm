package main

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

const (
	firstQuery  = 100
	sectionMark = "@@lazyslurm "
)

// refresh reads the journal and asks the host for states and snapshots, keeping prev where a step fails.
func refresh(root string, cfg Config, prev fresh) (fresh, error) {
	f := fresh{jobs: prev.jobs, states: prev.states, snaps: prev.snaps, commit: prev.commit}
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
	script := refreshScript(cfg.Target, query)
	out, err := cfg.Target.run(script, nil)
	if err != nil {
		return f, fmt.Errorf("read %s: %w", cfg.Host, err)
	}
	sec := sections(string(out))
	queued := parseSqueue(sec["squeue"])
	maps.Copy(f.states, judge(query, parseSacct(sec["sacct"]), queued, time.Now()))
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
	dir := strings.TrimSpace(sec["dir"])
	snaps := parseSizes(sec["sizes"])
	var owned []Job
	var unknown []Job
	for _, j := range journal {
		if j.Host != cfg.Host || !slices.ContainsFunc(snaps, func(s snapSize) bool { return ofCommit(j, s.Commit) }) {
			continue
		}
		owned = append(owned, j)
		if _, ok := f.states[j.Job]; !ok {
			unknown = append(unknown, j)
		}
	}
	if len(unknown) > 0 {
		states, _, err := fetchStates(cfg.Target, unknown, time.Now())
		if err != nil {
			return f, fmt.Errorf("states on %s: %w", cfg.Host, err)
		}
		maps.Copy(f.states, states)
	}
	var dirs []string
	for _, d := range queued {
		dirs = append(dirs, d...)
	}
	f.snaps = classify(snaps, owned, f.states, dirs, dir, strayOf(root, cfg, sec["files"]))
	f.commit, err = fullCommits(root, f.jobs, prev.commit)
	return f, err
}

func fullCommits(root string, jobs []Job, prev map[string]string) (map[string]string, error) {
	var asked []string
	for _, j := range jobs {
		if _, ok := prev[j.Commit]; j.Commit != "" && !ok && !slices.Contains(asked, j.Commit) {
			asked = append(asked, j.Commit)
		}
	}
	if len(asked) == 0 {
		return prev, nil
	}
	c := command("git", "cat-file", "--batch-check=%(objectname)")
	c.Dir = root
	c.Stdin = strings.NewReader(strings.Join(asked, "\n") + "\n")
	out, err := c.Output()
	if err != nil {
		return prev, fmt.Errorf("full commit hashes: %w", err)
	}
	full := maps.Clone(prev)
	if full == nil {
		full = map[string]string{}
	}
	for i, line := range strings.Split(string(out), "\n") {
		if i < len(asked) && commitName.MatchString(line) {
			full[asked[i]] = line
		}
	}
	return full, nil
}

func sections(out string) map[string]string {
	found := map[string]string{}
	for _, part := range strings.Split(out, sectionMark)[1:] {
		name, body, _ := strings.Cut(part, "\n")
		found[name] = body
	}
	return found
}

// refreshScript is the one command that reads the states, the queue and the snapshots of a target.
func refreshScript(t Target, query []Job) string {
	dir := quoteDir(t.Dir)
	var b strings.Builder
	b.WriteString("rc=0; ")
	if len(query) > 0 {
		ids := make([]string, len(query))
		for i, j := range query {
			ids[i] = j.Job
		}
		fmt.Fprintf(&b, "echo '%ssacct'; sacct -j %s -X -n -P --format=JobID,State || rc=1; ", sectionMark, quote(strings.Join(ids, ",")))
	}
	fmt.Fprintf(&b, "echo '%ssqueue'; squeue -h -u \"$(id -un)\" -o '%%i|%%Z' || rc=1; ", sectionMark)
	fmt.Fprintf(&b, "echo '%sdir'; mkdir -p %s && cd %s && pwd || exit 1; ", sectionMark, dir, dir)
	fmt.Fprintf(&b, "echo '%ssizes'; %s || rc=1; ", sectionMark, sizesScript)
	fmt.Fprintf(&b, "echo '%sfiles'; for c in $(%s); do find \"$c\" -type f -print0; done || rc=1; exit $rc", sectionMark, commitDirs)
	return b.String()
}
