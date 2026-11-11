package main

import (
	"cmp"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
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
	commit map[string]string
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
		return glyph{"✓", green, 2}
	}
	return glyph{"✗", red, 4}
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

func compactCounts(states map[string]int) string {
	var parts []string
	var last glyph
	n := 0
	flush := func() {
		if n > 0 {
			parts = append(parts, paint(last.color, strconv.Itoa(n)))
		}
	}
	for _, name := range sortedStates(states) {
		g := stateGlyph(name)
		if g != last {
			flush()
			last, n = g, 0
		}
		n += states[name]
	}
	flush()
	if len(parts) == 0 {
		return ""
	}
	return "[" + strings.Join(parts, "/") + "]"
}

func namedCounts(states map[string]int) string {
	var parts []string
	for _, name := range sortedStates(states) {
		g := stateGlyph(name)
		parts = append(parts, paint(g.color, fmt.Sprintf("%s %d %s", g.mark, states[name], name)))
	}
	return strings.Join(parts, "  ")
}

var escapes = regexp.MustCompile("\x1b\\[[0-9;]*m")

func plainText(s string) string { return escapes.ReplaceAllString(s, "") }

func field(name, value string) string {
	return paint(blue, fmt.Sprintf("%-9s", name)) + " " + value + "\n"
}

func ellipsize(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	if n < 5 {
		return string(runes[:max(n, 0)])
	}
	head := (n - 3) / 2
	tail := n - 3 - head
	return string(runes[:head]) + "..." + string(runes[len(runes)-tail:])
}

func (m *model) jobLine(j Job, width, idWidth, countsWidth int) string {
	counts := compactCounts(m.states[j.Job].States)
	counts = strings.Repeat(" ", countsWidth-utf8.RuneCountInString(plainText(counts))) + counts
	room := width - idWidth - countsWidth - 4
	name := ""
	if room > 0 {
		name = ellipsize(filepath.Base(j.Script), room)
		name += strings.Repeat(" ", room-utf8.RuneCountInString(name))
	}
	return paint(yellow, j.Job+strings.Repeat(" ", idWidth-len(j.Job))) + "  " + name + "  " + counts
}

// when formats an RFC 3339 stamp in local time as "15:37 Wed 11 Nov 2026", or returns it unchanged.
func when(stamp string) string {
	t, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return stamp
	}
	return t.Local().Format("15:04 Mon 2 Jan 2006")
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
			lines[i] = paint(green, "*") + " " + h
		}
	}
	return lines
}

func (m *model) jobLines(width int) []string {
	jobs := m.visibleJobs()
	idWidth, countsWidth := 0, 0
	for _, j := range jobs {
		idWidth = max(idWidth, len(j.Job))
		countsWidth = max(countsWidth, utf8.RuneCountInString(plainText(compactCounts(m.states[j.Job].States))))
	}
	lines := make([]string, len(jobs))
	for i, j := range jobs {
		lines[i] = m.jobLine(j, width, idWidth, countsWidth)
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
		b.WriteString(field("time", when(j.Time)))
		b.WriteString(field("script", j.Script+" "+strings.Join(j.Args, " ")))
		b.WriteString(field("commit", cmp.Or(m.commit[j.Commit], j.Commit)))
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
