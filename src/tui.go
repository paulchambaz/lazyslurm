package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jesseduffield/gocui"
)

const (
	popupView    = "popup"
	optionsView  = "options"
	mainView     = "main"
	logView      = "log"
	refreshEvery = 120 * time.Second
	logKept      = 500
	logTail      = 200
)

var panelViews = [panelCount]string{"hosts", "jobs", "snaps"}

var panelTitles = [panelCount]string{"[1]-Hosts", "[2]-Jobs", "[3]-Snapshots"}

const helpText = ` 1 2 3  [ ]  tab   go to a panel
 j k  arrows       move
 enter             select a host, or read the log of a job
 J K               scroll the main view
 /                 filter the jobs
 R                 refresh now
 P  p              push, pull
 s                 submit a script
 :                 run a command with srun in work/
 r  c              retry, cancel the selected job
 l                 read the log of the selected job
 d                 drop the selected snapshot
 q  ctrl-c         quit
`

type popupKind int

const (
	confirmPopup popupKind = iota
	inputPopup
	pickPopup
	helpPopup
)

type popup struct {
	id    int
	kind  popupKind
	title string
	text  string
	items []string
	sel   int
	ok    func(string)
}

type logbuf struct {
	mu    sync.Mutex
	lines []string
	part  string
	wake  func()
}

func (l *logbuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	l.part += string(p)
	for {
		line, rest, ok := strings.Cut(l.part, "\n")
		if !ok {
			break
		}
		l.lines = append(l.lines, line)
		l.part = rest
	}
	l.lines = l.lines[max(0, len(l.lines)-logKept):]
	l.mu.Unlock()
	if l.wake != nil {
		l.wake()
	}
	return len(p), nil
}

func (l *logbuf) add(line string) {
	fmt.Fprintln(l, line)
}

func (l *logbuf) tail(n int) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.lines[max(0, len(l.lines)-n):]...)
}

type tui struct {
	g      *gocui.Gui
	m      *model
	log    *logbuf
	pop    *popup
	drawn  int
	ids    int
	wg     sync.WaitGroup
	post   func(func())
	timer  *time.Timer
	auto   bool
	scroll int
}

func newTUI(g *gocui.Gui, root string, hosts []string, host string, cfg Config) *tui {
	u := &tui{g: g, log: &logbuf{}}
	u.m = &model{root: root, hosts: hosts, host: host, cfg: cfg, focus: jobsPanel, logs: map[string]string{}}
	u.post = func(f func()) {
		g.Update(func(*gocui.Gui) error {
			f()
			return nil
		})
	}
	u.log.wake = func() { u.post(func() {}) }
	for i, h := range hosts {
		if h == host {
			u.m.sel[hostsPanel] = i
		}
	}
	return u
}

func runTUI(root, hostFlag string) error {
	hosts, host, err := hostList(root, hostFlag)
	if err != nil {
		return err
	}
	cfg, err := loadConfig(root, host)
	if err != nil {
		return err
	}
	g, err := gocui.NewGui(gocui.NewGuiOpts{OutputMode: gocui.OutputTrue})
	if err != nil {
		return err
	}
	defer g.Close()
	u := newTUI(g, root, hosts, host, cfg)
	u.auto = true
	hookOut, trace = u.log, u.trace
	defer func() { hookOut, trace = os.Stderr, nil }()
	g.SetManagerFunc(u.layout)
	if err := u.bind(); err != nil {
		return err
	}
	u.start("refresh", nil, nil)
	if err := g.MainLoop(); err != nil && !errors.Is(err, gocui.ErrQuit) {
		return err
	}
	return nil
}

func (u *tui) trace(line string) {
	line = strings.ReplaceAll(strings.Join(strings.Fields(line), " "), `'\''`, `'`)
	u.log.add("$ " + strings.Replace(line, "ssh -o BatchMode=yes ", "ssh ", 1))
}

func (u *tui) idle() bool {
	if u.m.busy {
		u.log.add("busy: wait for the running task")
	}
	return !u.m.busy
}

// start runs work in the background, then refreshes; apply runs on the UI side when both are done.
func (u *tui) start(label string, work func() error, apply func()) {
	if u.m.busy {
		if label != "refresh" {
			u.log.add("busy: " + label + " not started")
		}
		return
	}
	u.m.busy = true
	root, cfg, prev := u.m.root, u.m.cfg, u.m.fresh
	u.wg.Add(1)
	go func() {
		defer u.wg.Done()
		var err error
		if work != nil {
			err = work()
		}
		f, rerr := refresh(root, cfg, prev)
		u.post(func() { u.finish(label, err, f, rerr, apply) })
	}()
}

func (u *tui) finish(label string, err error, f fresh, rerr error, apply func()) {
	u.m.busy = false
	u.m.fresh = f
	if rerr == nil {
		u.m.at = time.Now()
	}
	u.m.clamp()
	if apply != nil {
		apply()
	}
	if err != nil {
		u.log.add("error: " + label + ": " + err.Error())
	}
	if rerr != nil {
		u.log.add("error: refresh: " + rerr.Error())
	}
	if u.auto {
		if u.timer != nil {
			u.timer.Stop()
		}
		u.timer = time.AfterFunc(refreshEvery, func() {
			u.post(func() { u.start("refresh", nil, nil) })
		})
	}
}

func (u *tui) verb(label string, run func(w io.Writer) error) {
	u.start(label, func() error { return run(u.log) }, nil)
}

func (u *tui) open(p *popup) {
	u.ids++
	p.id = u.ids
	u.pop = p
}

func (u *tui) confirm(text string, then func()) {
	u.open(&popup{kind: confirmPopup, title: "Confirm", text: text, ok: func(string) { then() }})
}

func (u *tui) collect(titles, got []string, done func([]string)) {
	if len(got) == len(titles) {
		done(got)
		return
	}
	u.open(&popup{kind: inputPopup, title: titles[len(got)], ok: func(s string) {
		u.collect(titles, append(got, s), done)
	}})
}

func (u *tui) focusOn(p panel) error {
	u.m.focus, u.scroll = p, 0
	return nil
}

func (u *tui) cycle(delta int) error {
	return u.focusOn((u.m.focus + panel(delta) + panelCount) % panelCount)
}

func (u *tui) move(delta int) error {
	u.m.move(delta)
	u.scroll = 0
	return nil
}

func (u *tui) scrollMain(delta int) error {
	u.scroll = max(0, u.scroll+delta)
	return nil
}

func (u *tui) enter() error {
	switch u.m.focus {
	case hostsPanel:
		return u.selectHost()
	case jobsPanel:
		return u.logsOf()
	}
	return nil
}

func (u *tui) selectHost() error {
	if !u.idle() {
		return nil
	}
	name := u.m.hosts[u.m.sel[hostsPanel]]
	cfg, err := loadConfig(u.m.root, name)
	if err != nil {
		u.log.add("host " + name + ": " + err.Error())
		return nil
	}
	u.m.host, u.m.cfg, u.m.fresh = name, cfg, fresh{}
	u.m.sel[jobsPanel], u.m.sel[snapsPanel] = 0, 0
	u.m.logs = map[string]string{}
	u.start("refresh", nil, nil)
	return nil
}

func (u *tui) refreshNow() error {
	u.start("refresh", nil, nil)
	return nil
}

func (u *tui) push() error {
	root, host := u.m.root, u.m.host
	u.verb("push", func(w io.Writer) error { return runPush(w, root, host, false, nil) })
	return nil
}

func (u *tui) pull() error {
	root, host := u.m.root, u.m.host
	u.verb("pull", func(w io.Writer) error { return runPull(w, root, host, false, nil) })
	return nil
}

func (u *tui) submit() error {
	if !u.idle() {
		return nil
	}
	if len(u.m.cfg.Scripts) == 0 {
		u.log.add("submit: no scripts in .slurm.toml")
		return nil
	}
	root, host := u.m.root, u.m.host
	u.open(&popup{kind: pickPopup, title: "Script", items: u.m.cfg.Scripts, ok: func(script string) {
		u.collect([]string{"Arguments", "sbatch options", "Note"}, nil, func(a []string) {
			u.confirm("submit "+script+" on "+host+"?", func() {
				u.verb("submit", func(w io.Writer) error {
					return runSubmit(w, root, host, confirmer{Yes: true}, false, a[1], a[2], append([]string{script}, strings.Fields(a[0])...))
				})
			})
		})
	}})
	return nil
}

func (u *tui) retry() error {
	j, ok := u.m.job()
	if !ok || !u.idle() {
		return nil
	}
	root, host := u.m.root, u.m.host
	u.confirm("retry job "+j.Job+" on "+j.Host+"?", func() {
		u.verb("retry", func(w io.Writer) error {
			return runRetry(w, root, host, confirmer{Yes: true}, false, []string{j.Job})
		})
	})
	return nil
}

func (u *tui) cancel() error {
	j, ok := u.m.job()
	if !ok || !u.idle() {
		return nil
	}
	root, host := u.m.root, u.m.host
	u.confirm("cancel job "+j.Job+" on "+j.Host+"?", func() {
		u.verb("cancel", func(w io.Writer) error {
			return runCancel(w, root, host, confirmer{Yes: true}, false, []string{j.Job})
		})
	})
	return nil
}

func (u *tui) drop() error {
	s, ok := u.m.snap()
	if !ok || !u.idle() {
		return nil
	}
	root, host := u.m.root, u.m.host
	u.confirm("drop snapshot "+short(s.Commit)+" on "+host+"?", func() {
		u.verb("drop", func(w io.Writer) error {
			return runDrop(w, root, host, confirmer{Yes: true}, false, []string{s.Commit})
		})
	})
	return nil
}

func (u *tui) logsOf() error {
	j, ok := u.m.job()
	if !ok || !u.idle() {
		return nil
	}
	root, host, cfg := u.m.root, u.m.host, u.m.cfg
	var text string
	u.start("logs", func() error {
		var b strings.Builder
		rows, err := taskRows(cfg.Target, j.Job)
		if err != nil {
			fmt.Fprintf(&b, "tasks: %v\n", err)
		}
		if len(rows) > 1 {
			for _, r := range rows[:min(len(rows), 40)] {
				fmt.Fprintf(&b, "%s  %s\n", r.ID, r.State)
			}
		}
		var out strings.Builder
		err = runLogs(&out, root, host, false, false, []string{j.Job})
		lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
		b.WriteString("\n" + strings.Join(lines[max(0, len(lines)-logTail):], "\n"))
		text = b.String()
		return err
	}, func() {
		u.m.logs[j.Job] = text
		u.scroll = 1 << 30
	})
	return nil
}

func (u *tui) filter() error {
	u.open(&popup{kind: inputPopup, title: "Filter", text: u.m.filter, ok: func(s string) {
		u.m.filter = s
		u.m.clamp()
	}})
	return nil
}

func (u *tui) help() error {
	u.open(&popup{kind: helpPopup, title: "Keys", text: helpText})
	return nil
}

func (u *tui) exec() error {
	if !u.idle() {
		return nil
	}
	u.open(&popup{kind: inputPopup, title: "Command in work/", ok: func(line string) {
		if line == "" {
			return
		}
		hookOut, trace = os.Stderr, nil
		defer func() {
			hookOut, trace = u.log, u.trace
		}()
		if err := u.g.Suspend(); err != nil {
			u.log.add("exec: " + err.Error())
			return
		}
		err := runExec(os.Stdout, u.m.root, u.m.host, []string{line})
		if err != nil {
			fmt.Fprintln(os.Stderr, "lazyslurm:", err)
		}
		fmt.Fprint(os.Stderr, "\n[enter to return] ")
		bufio.NewReader(os.Stdin).ReadString('\n')
		if err := u.g.Resume(); err != nil {
			u.log.add("exec: " + err.Error())
		}
	}})
	return nil
}

func quit() error { return gocui.ErrQuit }

func (u *tui) bind() error {
	keys := []struct {
		key any
		fn  func() error
	}{
		{'1', func() error { return u.focusOn(hostsPanel) }},
		{'2', func() error { return u.focusOn(jobsPanel) }},
		{'3', func() error { return u.focusOn(snapsPanel) }},
		{'[', func() error { return u.cycle(-1) }},
		{']', func() error { return u.cycle(1) }},
		{gocui.KeyTab, func() error { return u.cycle(1) }},
		{'j', func() error { return u.move(1) }},
		{gocui.KeyArrowDown, func() error { return u.move(1) }},
		{'k', func() error { return u.move(-1) }},
		{gocui.KeyArrowUp, func() error { return u.move(-1) }},
		{'J', func() error { return u.scrollMain(5) }},
		{'K', func() error { return u.scrollMain(-5) }},
		{gocui.KeyEnter, u.enter},
		{'/', u.filter},
		{'?', u.help},
		{'R', u.refreshNow},
		{'P', u.push},
		{'p', u.pull},
		{'s', u.submit},
		{':', u.exec},
		{'r', u.retry},
		{'c', u.cancel},
		{'l', u.logsOf},
		{'d', u.drop},
		{'q', quit},
	}
	for _, k := range keys {
		for _, name := range panelViews {
			fn := k.fn
			if err := u.g.SetKeybinding(name, k.key, gocui.ModNone, func(*gocui.Gui, *gocui.View) error { return fn() }); err != nil {
				return err
			}
		}
	}
	if err := u.g.SetKeybinding("", gocui.KeyCtrlC, gocui.ModNone, func(*gocui.Gui, *gocui.View) error { return quit() }); err != nil {
		return err
	}
	for _, k := range []struct {
		key any
		fn  func(*gocui.View) error
	}{
		{'y', func(*gocui.View) error { return u.yes() }},
		{'n', func(*gocui.View) error { return u.dismiss() }},
		{'q', func(*gocui.View) error { return u.dismiss() }},
		{'?', func(*gocui.View) error { return u.dismiss() }},
		{gocui.KeyEsc, func(*gocui.View) error { return u.dismiss() }},
		{gocui.KeyEnter, u.accept},
		{'j', func(*gocui.View) error { return u.pick(1) }},
		{gocui.KeyArrowDown, func(*gocui.View) error { return u.pick(1) }},
		{'k', func(*gocui.View) error { return u.pick(-1) }},
		{gocui.KeyArrowUp, func(*gocui.View) error { return u.pick(-1) }},
	} {
		fn := k.fn
		if err := u.g.SetKeybinding(popupView, k.key, gocui.ModNone, func(_ *gocui.Gui, v *gocui.View) error { return fn(v) }); err != nil {
			return err
		}
	}
	return nil
}

func (u *tui) dismiss() error {
	u.pop = nil
	return nil
}

func (u *tui) yes() error {
	p := u.pop
	if p == nil || p.kind != confirmPopup {
		return nil
	}
	u.pop = nil
	p.ok("")
	return nil
}

func (u *tui) pick(delta int) error {
	if p := u.pop; p != nil && p.kind == pickPopup {
		p.sel = max(0, min(p.sel+delta, len(p.items)-1))
	}
	return nil
}

func (u *tui) accept(v *gocui.View) error {
	p := u.pop
	if p == nil {
		return nil
	}
	u.pop = nil
	switch p.kind {
	case inputPopup:
		p.ok(strings.TrimSpace(v.TextArea.GetContent()))
	case pickPopup:
		if len(p.items) > 0 {
			p.ok(p.items[p.sel])
		}
	}
	return nil
}

func unknownView(err error) bool {
	return err != nil && err.Error() == gocui.ErrUnknownView.Error()
}

func place(g *gocui.Gui, name string, x0, y0, x1, y1 int) (v *gocui.View, created bool, err error) {
	v, err = g.SetView(name, x0, y0, x1, y1, 0)
	if unknownView(err) {
		created, err = true, nil
	}
	if err != nil {
		return nil, false, err
	}
	v.FrameRunes = []rune{'─', '│', '╭', '╮', '╰', '╯'}
	return v, created, nil
}

func showList(v *gocui.View, lines []string, sel int) {
	v.Clear()
	for _, l := range lines {
		fmt.Fprintln(v, l)
	}
	v.Highlight = len(lines) > 0
	v.SelBgColor = gocui.ColorBlue
	oy := max(0, sel-v.InnerHeight()+1)
	v.SetOrigin(0, oy)
	v.SetCursor(0, sel-oy)
	v.Footer = ""
	if len(lines) > 0 {
		v.Footer = fmt.Sprintf("%d of %d", sel+1, len(lines))
	}
}

var panelKeys = [panelCount]string{
	"Select: enter | Push: P | Pull: p | Cmd: : | Refresh: R | Help: ?",
	"Log: l | Submit: s | Retry: r | Cancel: c | Push: P | Pull: p | Filter: / | Cmd: : | Help: ?",
	"Drop: d | Push: P | Pull: p | Cmd: : | Refresh: R | Help: ?",
}

func (u *tui) options(width int) string {
	left := panelKeys[u.m.focus]
	if u.m.filter != "" {
		left = "filter: " + u.m.filter + " | " + left
	}
	right := fmt.Sprintf("%d active", u.m.active())
	if !u.m.at.IsZero() {
		right += " | " + u.m.at.Format("15:04")
	}
	if u.m.busy {
		right = "working... | " + right
	}
	pad := width - len([]rune(left)) - len([]rune(right)) - 2
	if pad < 1 {
		return paint(cyan, left)
	}
	return paint(cyan, left) + strings.Repeat(" ", pad) + paint(grey, right)
}

func wrapLog(lines []string, width int) []string {
	var out []string
	for _, l := range lines {
		code := ""
		switch {
		case strings.HasPrefix(l, "$ "):
			code = cyan
		case strings.HasPrefix(l, "error: "):
			code = red
		case strings.HasPrefix(l, "busy: "):
			code = yellow
		}
		runes := []rune(l)
		for first := true; first || len(runes) > 0; first = false {
			n := min(len(runes), max(width, 1))
			chunk := string(runes[:n])
			runes = runes[n:]
			if code != "" {
				chunk = paint(code, chunk)
			}
			out = append(out, chunk)
		}
	}
	return out
}

func (u *tui) layout(g *gocui.Gui) error {
	w, h := g.Size()
	if w < 40 || h < 14 {
		return nil
	}
	m := u.m
	left := max(w/3, 24)
	rows := h - 1
	hostsEnd := min(len(m.hosts), 3) + 2
	free := rows - hostsEnd
	jobsEnd := hostsEnd + free*2/3
	if m.focus == snapsPanel {
		jobsEnd = hostsEnd + free/3
	}
	mainEnd := rows * 2 / 3
	g.Highlight, g.SelFrameColor = true, gocui.ColorGreen|gocui.AttrBold
	lists := [panelCount][]string{m.hostLines(), m.jobLines(), m.snapLines()}
	spans := [panelCount][2]int{{0, hostsEnd - 1}, {hostsEnd, jobsEnd - 1}, {jobsEnd, rows - 1}}
	for p := range panelCount {
		v, _, err := place(g, panelViews[p], 0, spans[p][0], left-1, spans[p][1])
		if err != nil {
			return err
		}
		v.Title = panelTitles[p]
		showList(v, lists[p], m.sel[p])
	}
	v, _, err := place(g, mainView, left, 0, w-1, mainEnd-1)
	if err != nil {
		return err
	}
	title, text := m.detail()
	v.Title = title
	v.Clear()
	fmt.Fprint(v, text)
	v.SetOrigin(0, min(u.scroll, max(0, strings.Count(text, "\n")+1-v.InnerHeight())))
	v, _, err = place(g, logView, left, mainEnd, w-1, rows-1)
	if err != nil {
		return err
	}
	v.Title = "Log"
	v.Clear()
	wrapped := wrapLog(u.log.tail(logTail), v.InnerWidth())
	fmt.Fprint(v, strings.Join(wrapped[max(0, len(wrapped)-v.InnerHeight()):], "\n"))
	v, _, err = place(g, optionsView, -1, h-2, w, h)
	if err != nil {
		return err
	}
	v.Frame = false
	v.Clear()
	fmt.Fprint(v, u.options(w))
	if err := u.drawPopup(g, w, h); err != nil {
		return err
	}
	name := panelViews[m.focus]
	if u.pop != nil {
		name = popupView
	}
	_, err = g.SetCurrentView(name)
	return err
}

func (u *tui) drawPopup(g *gocui.Gui, w, h int) error {
	p := u.pop
	if p == nil || p.id != u.drawn {
		if err := g.DeleteView(popupView); err != nil && !unknownView(err) {
			return err
		}
		u.drawn = 0
	}
	if p == nil {
		return nil
	}
	width, height := min(w-4, 64), 3
	switch p.kind {
	case pickPopup:
		height = min(len(p.items), 8) + 2
	case helpPopup:
		height = strings.Count(p.text, "\n") + 2
	case confirmPopup:
		width = min(w-4, max(len(p.text)+9, 30))
	}
	x0, y0 := (w-width)/2, max(0, (h-height)/2)
	v, created, err := place(g, popupView, x0, y0, x0+width-1, y0+height-1)
	if err != nil {
		return err
	}
	u.drawn = p.id
	v.Title = p.title
	switch p.kind {
	case confirmPopup:
		v.Clear()
		fmt.Fprint(v, p.text+" [y/N]")
	case inputPopup:
		if created {
			v.Editable, v.Editor = true, gocui.DefaultEditor
			v.TextArea.TypeString(p.text)
			v.RenderTextArea()
		}
	case pickPopup:
		showList(v, p.items, p.sel)
	case helpPopup:
		v.Clear()
		fmt.Fprint(v, p.text)
	}
	_, err = g.SetViewOnTop(popupView)
	return err
}
