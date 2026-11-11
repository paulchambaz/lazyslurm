package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type panel int

const (
	hostsPanel panel = iota
	jobsPanel
	snapsPanel
	panelCount
)

// fresh is what a refresh reads from the journal and from the host.
type fresh struct {
	jobs   []Job
	states map[string]jobState
	snaps  []snapshotInfo
}

type model struct {
	fresh
	root   string
	hosts  []string
	host   string
	cfg    Config
	sel    [panelCount]int
	focus  panel
	filter string
	busy   bool
	logs   map[string]string
	at     time.Time
}

func (m *model) visibleJobs() []Job {
	if m.filter == "" {
		return m.jobs
	}
	needle := strings.ToLower(m.filter)
	var shown []Job
	for _, j := range m.jobs {
		if strings.Contains(strings.ToLower(j.Job+" "+j.Script+" "+j.Note), needle) {
			shown = append(shown, j)
		}
	}
	return shown
}

func (m *model) length(p panel) int {
	switch p {
	case hostsPanel:
		return len(m.hosts)
	case jobsPanel:
		return len(m.visibleJobs())
	}
	return len(m.snaps)
}

func (m *model) clamp() {
	for p := range panelCount {
		m.sel[p] = max(0, min(m.sel[p], m.length(p)-1))
	}
}

func (m *model) move(delta int) {
	m.sel[m.focus] += delta
	m.clamp()
}

func (m *model) job() (Job, bool) {
	jobs := m.visibleJobs()
	if m.sel[jobsPanel] >= len(jobs) {
		return Job{}, false
	}
	return jobs[m.sel[jobsPanel]], true
}

func (m *model) snap() (snapshotInfo, bool) {
	if m.sel[snapsPanel] >= len(m.snaps) {
		return snapshotInfo{}, false
	}
	return m.snaps[m.sel[snapsPanel]], true
}

const (
	grey   = "90"
	red    = "31"
	green  = "32"
	yellow = "33"
	cyan   = "36"
)

func paint(code, s string) string {
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func stateColor(state string) string {
	switch {
	case strings.HasPrefix(state, "COMPLETED"):
		return green
	case strings.HasPrefix(state, "RUNNING"), strings.HasPrefix(state, "COMPLETING"):
		return cyan
	case strings.HasPrefix(state, "PENDING"), strings.HasPrefix(state, "CONFIGURING"), strings.HasPrefix(state, "REQUEUED"):
		return yellow
	}
	return red
}

func coloredCounts(states map[string]int) string {
	names := make([]string, 0, len(states))
	for name := range states {
		names = append(names, name)
	}
	slices.Sort(names)
	parts := make([]string, len(names))
	for i, name := range names {
		parts[i] = paint(stateColor(name), fmt.Sprintf("%d %s", states[name], name))
	}
	return strings.Join(parts, "  ")
}

func shortTime(t string) string {
	if len(t) < 16 {
		return t
	}
	return t[5:10] + " " + t[11:16]
}

func (m *model) jobLine(j Job) string {
	line := j.Job + "  " + paint(grey, shortTime(j.Time)) + "  " + filepath.Base(j.Script)
	if st, ok := m.states[j.Job]; ok && len(st.States) > 0 {
		line += "  " + coloredCounts(st.States)
	}
	return line
}

func (m *model) active() int {
	n := 0
	for _, st := range m.states {
		if !st.Final {
			n++
		}
	}
	return n
}

func snapLine(s snapshotInfo) string {
	return fmt.Sprintf("%s  %s  %d/%d  %s", short(s.Commit), human(s.KB), s.Active, s.Total, s.State)
}

func (m *model) hostLines() []string {
	lines := make([]string, len(m.hosts))
	for i, h := range m.hosts {
		lines[i] = "  " + h
		if h == m.host {
			lines[i] = "* " + h
		}
	}
	return lines
}

func (m *model) jobLines() []string {
	jobs := m.visibleJobs()
	lines := make([]string, len(jobs))
	for i, j := range jobs {
		lines[i] = m.jobLine(j)
	}
	return lines
}

func (m *model) snapLines() []string {
	lines := make([]string, len(m.snaps))
	for i, s := range m.snaps {
		lines[i] = snapLine(s)
	}
	return lines
}

// detail describes the selection of the focused panel for the main view.
func (m *model) detail() (title, text string) {
	var b strings.Builder
	switch m.focus {
	case hostsPanel:
		if m.sel[hostsPanel] >= len(m.hosts) || m.hosts[m.sel[hostsPanel]] != m.host {
			return "Host", "enter selects this host\n"
		}
		fmt.Fprintf(&b, "host      %s\ntarget    %s\n", m.host, m.cfg.Target.path(m.cfg.Target.Dir))
		fmt.Fprintf(&b, "push      %s\npull      %s\n", strings.Join(m.cfg.Push, " "), strings.Join(m.cfg.Pull, " "))
		fmt.Fprintf(&b, "scripts   %s\nsrun      %s\n", strings.Join(m.cfg.Scripts, " "), m.cfg.Srun)
		return "Host " + m.host, b.String()
	case jobsPanel:
		j, ok := m.job()
		if !ok {
			return "Job", ""
		}
		fmt.Fprintf(&b, "job       %s on %s\ntime      %s\n", j.Job, j.Host, j.Time)
		fmt.Fprintf(&b, "script    %s %s\ncommit    %s\n", j.Script, strings.Join(j.Args, " "), j.Commit)
		for _, f := range []struct{ name, value string }{
			{"sbatch", j.Sbatch}, {"note", j.Note}, {"array", j.Array}, {"retry of", j.RetryOf},
		} {
			if f.value != "" {
				fmt.Fprintf(&b, "%-9s %s\n", f.name, f.value)
			}
		}
		if st, ok := m.states[j.Job]; ok {
			fmt.Fprintf(&b, "states    %s\nfinal     %t\n", countsText(st.States), st.Final)
		}
		if text, ok := m.logs[j.Job]; ok {
			b.WriteString("\n" + text)
		}
		return "Job " + j.Job, b.String()
	}
	s, ok := m.snap()
	if !ok {
		return "Snapshot", ""
	}
	fmt.Fprintf(&b, "commit    %s\nsize      %s\njobs      %d active of %d\nstate     %s\n",
		s.Commit, human(s.KB), s.Active, s.Total, s.State)
	for _, f := range s.Stray {
		fmt.Fprintf(&b, "stray     %s\n", f)
	}
	return "Snapshot " + short(s.Commit), b.String()
}
