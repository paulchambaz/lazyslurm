package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

var commitName = regexp.MustCompile(`^[0-9a-f]{40}$`)

type snapState string

const (
	stateActive snapState = "active"
	stateFinal  snapState = "final"
	stateStray  snapState = "stray"
)

type snapSize struct {
	Commit string
	KB     int
}

type snapshotInfo struct {
	snapSize
	Active int
	Total  int
	State  snapState
	Stray  []string
}

func ofCommit(j Job, commit string) bool {
	return j.Commit != "" && strings.HasPrefix(commit, j.Commit)
}

// listSnapshots lists the snapshots of dir, oldest first, with the disk each one costs beyond work/.
func listSnapshots(t Target, dir string) ([]snapSize, error) {
	out, err := t.run(fmt.Sprintf(
		`cd %s && { [ -d work ] && echo work; ls -tr | grep -E '^[0-9a-f]{40}$'; true; } | xargs -r du -sk --`,
		quote(dir)), nil)
	if err != nil {
		return nil, fmt.Errorf("list snapshots: %w", err)
	}
	var snaps []snapSize
	for line := range strings.SplitSeq(string(out), "\n") {
		size, name, ok := strings.Cut(line, "\t")
		kb, err := strconv.Atoi(size)
		if !ok || err != nil || !commitName.MatchString(name) {
			continue
		}
		snaps = append(snaps, snapSize{name, kb})
	}
	return snaps, nil
}

func treeFiles(root string, cfg Config, commit string) map[string]bool {
	c := command("git", append([]string{"ls-tree", "-r", "--name-only", "-z", commit, "--"}, cfg.Push...)...)
	c.Dir = root
	out, err := c.Output()
	files := map[string]bool{}
	if err != nil {
		return files
	}
	for name := range strings.SplitSeq(string(out), "\x00") {
		files[name] = true
	}
	return files
}

func collected(rel string) bool {
	for _, pattern := range []string{"slurm-*.out", "slurm-*.err"} {
		if ok, err := filepath.Match(pattern, rel); err == nil && ok {
			return true
		}
	}
	return false
}

// strayFiles lists, by commit, the files of each snapshot that the commit and the keep paths do not explain.
func strayFiles(root string, cfg Config, dir string, commits []string) (map[string][]string, error) {
	stray := map[string][]string{}
	if len(commits) == 0 {
		return stray, nil
	}
	out, err := cfg.Target.run(fmt.Sprintf(
		`cd %s && for c in %s; do find "$c" -type f -print0; done`, quote(dir), quoteAll(commits)), nil)
	if err != nil {
		return nil, fmt.Errorf("list snapshot files: %w", err)
	}
	expected := map[string]map[string]bool{}
	for path := range strings.SplitSeq(string(out), "\x00") {
		commit, rel, ok := strings.Cut(path, "/")
		if !ok {
			continue
		}
		if expected[commit] == nil {
			expected[commit] = treeFiles(root, cfg, commit)
		}
		if expected[commit][rel] || excluded(rel, cfg.Exclude) || collected(rel) {
			continue
		}
		stray[commit] = append(stray[commit], rel)
	}
	for _, files := range stray {
		slices.Sort(files)
	}
	return stray, nil
}

// classify applies the deletion rules of the design to each snapshot of dir.
func classify(snaps []snapSize, jobs []Job, states map[string]jobState, queue []string, dir string, stray map[string][]string) []snapshotInfo {
	infos := make([]snapshotInfo, len(snaps))
	for i, s := range snaps {
		info := snapshotInfo{snapSize: s, Stray: stray[s.Commit]}
		for _, j := range jobs {
			if !ofCommit(j, s.Commit) {
				continue
			}
			info.Total++
			if !states[j.Job].Final {
				info.Active++
			}
		}
		running := slices.ContainsFunc(queue, func(d string) bool { return inside(d, dir+"/"+s.Commit) })
		switch {
		case info.Active > 0 || running:
			info.State = stateActive
		case len(info.Stray) > 0:
			info.State = stateStray
		default:
			info.State = stateFinal
		}
		infos[i] = info
	}
	return infos
}

// loadSnapshots reads the snapshots of dir and judges each one; any failure is an error, so nothing gets deleted.
func loadSnapshots(root string, cfg Config, dir string) ([]snapshotInfo, error) {
	snaps, err := listSnapshots(cfg.Target, dir)
	if err != nil {
		return nil, err
	}
	journal, err := readJournal(root)
	if err != nil {
		return nil, err
	}
	var jobs []Job
	for _, j := range journal {
		if j.Host == cfg.Host && slices.ContainsFunc(snaps, func(s snapSize) bool { return ofCommit(j, s.Commit) }) {
			jobs = append(jobs, j)
		}
	}
	states, queue, err := fetchStates(cfg.Target, jobs, time.Now())
	if err != nil {
		return nil, err
	}
	commits := make([]string, len(snaps))
	for i, s := range snaps {
		commits[i] = s.Commit
	}
	stray, err := strayFiles(root, cfg, dir, commits)
	if err != nil {
		return nil, err
	}
	return classify(snaps, jobs, states, queue, dir, stray), nil
}

func deleteSnapshots(cfg Config, dir string, commits []string) error {
	if len(commits) == 0 {
		return nil
	}
	for _, c := range commits {
		if !commitName.MatchString(c) {
			return fmt.Errorf("delete snapshots: %q is not a commit hash", c)
		}
	}
	unlock, err := cfg.Target.lock(dir)
	if err != nil {
		return err
	}
	_, err = cfg.Target.run("cd "+quote(dir)+" && rm -rf -- "+quoteAll(commits), nil)
	if unlockErr := unlock(); err == nil {
		err = unlockErr
	}
	if err != nil {
		return fmt.Errorf("delete snapshots: %w", err)
	}
	return nil
}

func human(kb int) string {
	switch {
	case kb < 1024:
		return fmt.Sprintf("%dK", kb)
	case kb < 1024*1024:
		return fmt.Sprintf("%.1fM", float64(kb)/1024)
	}
	return fmt.Sprintf("%.1fG", float64(kb)/(1024*1024))
}

type snapshotRow struct {
	Host   string    `json:"host"`
	Commit string    `json:"commit"`
	KB     int       `json:"kb"`
	Active int       `json:"active"`
	Total  int       `json:"total"`
	State  snapState `json:"state"`
	Stray  []string  `json:"stray,omitempty"`
}

func runSnapshots(w io.Writer, root, hostFlag string, asJSON bool, args []string) error {
	if len(args) > 0 {
		return usageError("snapshots takes no arguments")
	}
	cfg, err := loadConfig(root, hostFlag)
	if err != nil {
		return err
	}
	dir, err := cfg.Target.resolve()
	if err != nil {
		return err
	}
	infos, err := loadSnapshots(root, cfg, dir)
	if err != nil {
		return fmt.Errorf("snapshots on %s: %w", cfg.Host, err)
	}
	for _, s := range infos {
		if asJSON {
			row := snapshotRow{cfg.Host, s.Commit, s.KB, s.Active, s.Total, s.State, s.Stray}
			if err := json.NewEncoder(w).Encode(row); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintf(w, "%s  %s  %d/%d  %s\n", s.Commit[:8], human(s.KB), s.Active, s.Total, s.State); err != nil {
			return err
		}
		for _, f := range s.Stray {
			if _, err := fmt.Fprintf(w, "    %s\n", f); err != nil {
				return err
			}
		}
	}
	return nil
}

type dropRow struct {
	Host   string `json:"host"`
	Commit string `json:"commit"`
}

func runDrop(w io.Writer, root, hostFlag string, ask confirmer, asJSON bool, args []string) error {
	if len(args) != 1 || args[0] == "" {
		return usageError("drop needs one commit")
	}
	cfg, err := loadConfig(root, hostFlag)
	if err != nil {
		return err
	}
	dir, err := cfg.Target.resolve()
	if err != nil {
		return err
	}
	infos, err := loadSnapshots(root, cfg, dir)
	if err != nil {
		return fmt.Errorf("snapshots on %s: %w", cfg.Host, err)
	}
	var match []snapshotInfo
	for _, s := range infos {
		if strings.HasPrefix(s.Commit, args[0]) {
			match = append(match, s)
		}
	}
	switch {
	case len(match) == 0:
		return fmt.Errorf("no snapshot of %s matches %s", cfg.Host, args[0])
	case len(match) > 1:
		return fmt.Errorf("%s matches %d snapshots: give more of the hash", args[0], len(match))
	case match[0].State == stateActive:
		return errors.New("a job is active in snapshot " + match[0].Commit[:8])
	}
	s := match[0]
	what := fmt.Sprintf("would drop snapshot %s on %s (%s)", s.Commit[:8], cfg.Host, human(s.KB))
	if len(s.Stray) > 0 {
		what += fmt.Sprintf(", losing %d stray files", len(s.Stray))
	}
	if err := ask.ask(what); err != nil {
		return err
	}
	if err := deleteSnapshots(cfg, dir, []string{s.Commit}); err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(w).Encode(dropRow{cfg.Host, s.Commit})
	}
	_, err = fmt.Fprintf(w, "dropped %s on %s\n", s.Commit[:8], cfg.Host)
	return err
}
