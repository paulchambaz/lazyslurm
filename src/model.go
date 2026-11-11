package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
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
	task   string
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
	grey    = "90"
	red     = "31"
	green   = "32"
	yellow  = "33"
	blue    = "34"
	magenta = "35"
	cyan    = "36"
)

func paint(code, s string) string {
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

type glyph struct {
	mark  string
	color string
	rank  int
}

func stateGlyph(state string) glyph {
	switch {
	case strings.HasPrefix(state, "RUNNING"), strings.HasPrefix(state, "COMPLETING"):
		return glyph{"●", cyan, 0}
	case strings.HasPrefix(state, "PENDING"), strings.HasPrefix(state, "CONFIGURING"), strings.HasPrefix(state, "REQUEUED"), strings.HasPrefix(state, "SUSPENDED"):
		return glyph{"◌", yellow, 1}
	case strings.HasPrefix(state, "CANCELLED"):
		return glyph{"⊘", magenta, 3}
	case strings.HasPrefix(state, "COMPLETED"):
		return glyph{"✓", green, 4}
	}
	return glyph{"✗", red, 2}
}

func sortedStates(states map[string]int) []string {
	names := make([]string, 0, len(states))
	for name := range states {
		names = append(names, name)
	}
	slices.SortFunc(names, func(a, b string) int {
		return stateGlyph(a).rank - stateGlyph(b).rank
	})
	return names
}

func jobMark(states map[string]int) string {
	names := sortedStates(states)
	if len(names) == 0 {
		return paint(grey, "·")
	}
	g := stateGlyph(names[0])
	return paint(g.color, g.mark)
}

func compactCounts(states map[string]int) string {
	var parts []string
	for _, name := range sortedStates(states) {
		g := stateGlyph(name)
		parts = append(parts, paint(g.color, g.mark+strconv.Itoa(states[name])))
	}
	return strings.Join(parts, " ")
}

func namedCounts(states map[string]int) string {
	var parts []string
	for _, name := range sortedStates(states) {
		g := stateGlyph(name)
		parts = append(parts, paint(g.color, fmt.Sprintf("%s %d %s", g.mark, states[name], name)))
	}
	return strings.Join(parts, "  ")
}

func field(name, value string) string {
	return paint(blue, fmt.Sprintf("%-9s", name)) + " " + value + "\n"
}

func (m *model) jobLine(j Job) string {
	line := jobMark(m.states[j.Job].States) + " " + paint(yellow, j.Job) + "  " + strings.TrimSuffix(filepath.Base(j.Script), ".sh")
	if st, ok := m.states[j.Job]; ok && len(st.States) > 0 {
		line += "  " + compactCounts(st.States)
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
	color := green
	switch s.State {
	case stateActive:
		color = cyan
	case stateStray:
		color = yellow
	}
	return fmt.Sprintf("%s  %s  %d/%d  %s", paint(yellow, short(s.Commit)), paint(grey, human(s.KB)), s.Active, s.Total, paint(color, string(s.State)))
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
		b.WriteString(field("host", m.host))
		b.WriteString(field("target", m.cfg.Target.path(m.cfg.Target.Dir)))
		b.WriteString(field("push", strings.Join(m.cfg.Push, " ")))
		b.WriteString(field("pull", strings.Join(m.cfg.Pull, " ")))
		b.WriteString(field("scripts", strings.Join(m.cfg.Scripts, " ")))
		b.WriteString(field("srun", m.cfg.Srun))
		return "Host " + m.host, b.String()
	case jobsPanel:
		j, ok := m.job()
		if !ok {
			return "Job", ""
		}
		b.WriteString(field("job", j.Job+" on "+j.Host))
		b.WriteString(field("time", j.Time))
		b.WriteString(field("script", j.Script+" "+strings.Join(j.Args, " ")))
		b.WriteString(field("commit", j.Commit))
		for _, f := range []struct{ name, value string }{
			{"sbatch", j.Sbatch}, {"note", j.Note}, {"array", j.Array}, {"retry of", j.RetryOf},
		} {
			if f.value != "" {
				b.WriteString(field(f.name, f.value))
			}
		}
		if st, ok := m.states[j.Job]; ok {
			b.WriteString(field("states", namedCounts(st.States)))
			b.WriteString(field("final", strconv.FormatBool(st.Final)))
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
	b.WriteString(field("commit", s.Commit))
	b.WriteString(field("size", human(s.KB)))
	b.WriteString(field("jobs", fmt.Sprintf("%d active of %d", s.Active, s.Total)))
	b.WriteString(field("state", string(s.State)))
	for _, f := range s.Stray {
		b.WriteString(field("stray", f))
	}
	return "Snapshot " + short(s.Commit), b.String()
}
