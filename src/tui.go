package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jesseduffield/gocui"
)

const (
	popupView     = "popup"
	suggestView   = "suggest"
	previewView   = "preview"
	optionsView   = "options"
	mainView      = "main"
	logView       = "log"
	refreshEvery  = 120 * time.Second
	logKept       = 500
	logTail       = 200
	spinEvery     = 180 * time.Millisecond
	promptWidth   = 80
	scriptWidth   = 90
	scriptHeight  = 20
	previewLines  = 2000
	suggestHeight = 12
	activeTitle   = gocui.ColorGreen | gocui.AttrBold
)

var spinFrames = []string{"●∙∙", "∙●∙", "∙∙●", "∙●∙"}

var taskNames = map[string]string{
	"push": "Pushing", "pull": "Pulling", "submit": "Submitting", "retry": "Retrying",
	"cancel": "Cancelling", "drop": "Dropping", "logs": "Reading logs", "refresh": "Refreshing",
}

var panelViews = [panelCount]string{"hosts", "jobs", "snaps"}

var panelTitles = [panelCount]string{"[1]-Hosts", "[2]-Jobs", "[3]-Snapshots"}

const helpText = ` 1 2 3  [ ]  tab   go to a panel
 j k  arrows       move
 enter             select a host, or read the log of a job
 0                 go to the main view, to scroll it with j k (esc to leave)
 J K               scroll the main view
 /                 filter the jobs
 R                 refresh now
 P  p              push, pull
 s                 submit a script
 :                 run a command with srun in work/
 r  c              retry, cancel the selected job
 L                 read the log of the selected job
 d                 drop the selected snapshot
 q  ctrl-c         quit
`

type popupKind int

const (
	confirmPopup popupKind = iota
	inputPopup
	helpPopup
	filterPopup
	scriptPopup
)

type popup struct {
	id    int
	kind  popupKind
	title string
	text  string
	sel   int
	ok    func(string)

	history []string
	matches []string
	focus   bool
	readY   int
	readX   int
	reading string
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
	inMain bool
	inLog  bool
	logUp  int
	past   []string
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
	u.past, err = readHistory(root)
	if err != nil {
		u.log.add("error: " + err.Error())
	}
	hookOut = u.log
	defer func() { hookOut = os.Stderr }()
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
	u.m.busy, u.m.task = true, label
	if u.auto {
		u.spin()
	}
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

func (u *tui) spin() {
	time.AfterFunc(spinEvery, func() {
		u.post(func() {
			if u.m.busy {
				u.spin()
			}
		})
	})
}

func (u *tui) finish(label string, err error, f fresh, rerr error, apply func()) {
	u.m.busy = false
	u.m.fresh = f
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
	u.m.focus, u.scroll, u.inMain, u.inLog = p, 0, false, false
	return nil
}

func (u *tui) focusMain() error {
	u.inMain, u.inLog = true, false
	return nil
}

func (u *tui) focusLog() error {
	u.inMain, u.inLog = false, true
	return nil
}

func (u *tui) back() error {
	if !u.inMain && !u.inLog && u.m.filter != "" {
		u.m.filter = ""
		u.m.clamp()
	}
	u.inMain, u.inLog = false, false
	return nil
}

func (u *tui) cycle(delta int) error {
	return u.focusOn((u.m.focus + panel(delta) + panelCount) % panelCount)
}

func (u *tui) move(delta int) error {
	if u.inMain || u.inLog {
		return u.scrollMain(delta)
	}
	u.m.move(delta)
	u.scroll = 0
	return nil
}

func (u *tui) scrollMain(delta int) error {
	if u.inLog {
		u.logUp = max(0, u.logUp-delta)
		return nil
	}
	u.scroll = max(0, u.scroll+delta)
	return nil
}

func (u *tui) scrollLog(delta int) error {
	u.logUp = max(0, u.logUp-delta)
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
	root, host := u.m.root, u.m.host
	scripts, err := scriptFiles(root, u.m.cfg.Scripts)
	if err != nil {
		u.log.add("error: submit: " + err.Error())
		return nil
	}
	if len(scripts) == 0 {
		u.log.add("error: submit: no file matches scripts = " + strings.Join(u.m.cfg.Scripts, " "))
		return nil
	}
	u.open(&popup{kind: scriptPopup, title: "Script:", history: scripts, ok: func(script string) {
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
	u.open(&popup{kind: filterPopup, text: u.m.filter})
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
	u.open(&popup{kind: inputPopup, title: "Command:", history: u.past, ok: func(line string) {
		if line == "" {
			return
		}
		u.past = append([]string{line}, slices.DeleteFunc(slices.Clone(u.past), func(c string) bool { return c == line })...)
		if err := addHistory(u.m.root, line); err != nil {
			u.log.add("error: " + err.Error())
		}
		hookOut = os.Stderr
		defer func() { hookOut = u.log }()
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
		{'L', u.logsOf},
		{'0', u.focusMain},
		{gocui.KeyEsc, u.back},
		{'d', u.drop},
		{'q', quit},
	}
	for _, k := range keys {
		for _, name := range append(panelViews[:], mainView, logView) {
			fn := k.fn
			if err := u.g.SetKeybinding(name, k.key, gocui.ModNone, func(*gocui.Gui, *gocui.View) error { return fn() }); err != nil {
				return err
			}
		}
	}
	if err := u.g.SetKeybinding("", gocui.KeyCtrlC, gocui.ModNone, func(*gocui.Gui, *gocui.View) error { return quit() }); err != nil {
		return err
	}
	if err := u.mouse(); err != nil {
		return err
	}
	if err := u.bindPreview(); err != nil {
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
		{gocui.KeyTab, func(*gocui.View) error { return u.focusSuggestions(true) }},
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
	for _, k := range []struct {
		key any
		fn  func() error
	}{
		{gocui.KeyTab, func() error { return u.focusSuggestions(false) }},
		{gocui.KeyEsc, func() error { return u.focusSuggestions(false) }},
		{gocui.KeyEnter, u.fill},
		{'j', func() error { return u.pick(1) }},
		{gocui.KeyArrowDown, func() error { return u.pick(1) }},
		{'k', func() error { return u.pick(-1) }},
		{gocui.KeyArrowUp, func() error { return u.pick(-1) }},
	} {
		fn := k.fn
		if err := u.g.SetKeybinding(suggestView, k.key, gocui.ModNone, func(*gocui.Gui, *gocui.View) error { return fn() }); err != nil {
			return err
		}
	}
	return nil
}

func (u *tui) click(p panel, y int, double bool) error {
	u.focusOn(p)
	if y >= 0 && y < u.m.length(p) {
		u.m.sel[p] = y
	}
	if double && y >= 0 && y < u.m.length(p) {
		return u.enter()
	}
	return nil
}

func (u *tui) wheel(p panel, delta int) error {
	u.m.sel[p] += delta
	u.m.clamp()
	if p == u.m.focus {
		u.scroll = 0
	}
	return nil
}

func (u *tui) clickSuggestion(y int) error {
	p := u.pop
	if p == nil || y < 0 || y >= len(p.matches) {
		return nil
	}
	p.sel = y
	switch p.kind {
	case inputPopup:
		return u.fill()
	case scriptPopup:
		u.pop = nil
		p.ok(p.matches[y])
	}
	return nil
}

func (u *tui) mouse() error {
	u.g.Mouse = true
	u.g.ShouldHandleMouseEvent = func(v *gocui.View, _ gocui.Key) bool {
		return u.pop == nil || v.Name() == popupView || v.Name() == suggestView || v.Name() == previewView
	}
	for p, name := range panelViews {
		for _, b := range []struct {
			key gocui.Key
			fn  func(gocui.ViewMouseBindingOpts) error
		}{
			{gocui.MouseLeft, func(o gocui.ViewMouseBindingOpts) error { return u.click(panel(p), o.Y, o.IsDoubleClick) }},
			{gocui.MouseWheelUp, func(gocui.ViewMouseBindingOpts) error { return u.wheel(panel(p), -1) }},
			{gocui.MouseWheelDown, func(gocui.ViewMouseBindingOpts) error { return u.wheel(panel(p), 1) }},
		} {
			if err := u.g.SetViewClickBinding(&gocui.ViewMouseBinding{ViewName: name, Key: b.key, Handler: b.fn}); err != nil {
				return err
			}
		}
	}
	for _, b := range []struct {
		view string
		key  gocui.Key
		fn   func(gocui.ViewMouseBindingOpts) error
	}{
		{mainView, gocui.MouseLeft, func(gocui.ViewMouseBindingOpts) error { return u.focusMain() }},
		{mainView, gocui.MouseWheelUp, func(gocui.ViewMouseBindingOpts) error { return u.scrollMain(-3) }},
		{mainView, gocui.MouseWheelDown, func(gocui.ViewMouseBindingOpts) error { return u.scrollMain(3) }},
		{logView, gocui.MouseLeft, func(gocui.ViewMouseBindingOpts) error { return u.focusLog() }},
		{logView, gocui.MouseWheelUp, func(gocui.ViewMouseBindingOpts) error { return u.scrollLog(-3) }},
		{logView, gocui.MouseWheelDown, func(gocui.ViewMouseBindingOpts) error { return u.scrollLog(3) }},
		{suggestView, gocui.MouseLeft, func(o gocui.ViewMouseBindingOpts) error { return u.clickSuggestion(o.Y) }},
		{previewView, gocui.MouseLeft, func(gocui.ViewMouseBindingOpts) error { return u.focusSuggestions(true) }},
		{previewView, gocui.MouseWheelUp, func(gocui.ViewMouseBindingOpts) error { return u.read(-3, 0) }},
		{previewView, gocui.MouseWheelDown, func(gocui.ViewMouseBindingOpts) error { return u.read(3, 0) }},
		{suggestView, gocui.MouseWheelUp, func(gocui.ViewMouseBindingOpts) error { return u.pick(-1) }},
		{suggestView, gocui.MouseWheelDown, func(gocui.ViewMouseBindingOpts) error { return u.pick(1) }},
	} {
		if err := u.g.SetViewClickBinding(&gocui.ViewMouseBinding{ViewName: b.view, Key: b.key, Handler: b.fn}); err != nil {
			return err
		}
	}
	return nil
}

func (u *tui) bindPreview() error {
	for _, k := range []struct {
		key any
		fn  func() error
	}{
		{gocui.KeyTab, func() error { return u.focusSuggestions(false) }},
		{gocui.KeyEsc, func() error { return u.focusSuggestions(false) }},
		{'j', func() error { return u.read(1, 0) }},
		{gocui.KeyArrowDown, func() error { return u.read(1, 0) }},
		{'k', func() error { return u.read(-1, 0) }},
		{gocui.KeyArrowUp, func() error { return u.read(-1, 0) }},
		{'J', func() error { return u.read(10, 0) }},
		{'K', func() error { return u.read(-10, 0) }},
		{'h', func() error { return u.read(0, -4) }},
		{gocui.KeyArrowLeft, func() error { return u.read(0, -4) }},
		{'l', func() error { return u.read(0, 4) }},
		{gocui.KeyArrowRight, func() error { return u.read(0, 4) }},
	} {
		fn := k.fn
		if err := u.g.SetKeybinding(previewView, k.key, gocui.ModNone, func(*gocui.Gui, *gocui.View) error { return fn() }); err != nil {
			return err
		}
	}
	return nil
}

func (u *tui) focusSuggestions(on bool) error {
	if p := u.pop; p != nil && (p.kind == inputPopup || p.kind == scriptPopup) && (!on || len(p.matches) > 0) {
		p.focus = on
	}
	return nil
}

func (u *tui) read(dy, dx int) error {
	if p := u.pop; p != nil && p.kind == scriptPopup {
		p.readY = max(0, p.readY+dy)
		p.readX = max(0, p.readX+dx)
	}
	return nil
}

func (u *tui) fill() error {
	p := u.pop
	if p == nil || p.sel >= len(p.matches) {
		return nil
	}
	p.focus = false
	if v, err := u.g.View(popupView); err == nil {
		v.ClearTextArea()
		v.TextArea.TypeString(p.matches[p.sel])
		v.RenderTextArea()
	}
	return nil
}

func (u *tui) dismiss() error {
	if u.pop != nil && u.pop.kind == filterPopup {
		u.m.filter = ""
		u.m.clamp()
	}
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
	p := u.pop
	switch {
	case p == nil:
	case p.kind == inputPopup, p.kind == scriptPopup:
		p.sel = max(0, min(p.sel+delta, len(p.matches)-1))
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
	case filterPopup:
		u.m.filter = strings.TrimSpace(v.TextArea.GetContent())
		u.m.clamp()
	case scriptPopup:
		if p.sel < len(p.matches) {
			p.ok(p.matches[p.sel])
		}
	case inputPopup:
		p.ok(strings.TrimSpace(v.TextArea.GetContent()))
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
		fmt.Fprintln(v, " "+l)
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
	"Log: L | Submit: s | Retry: r | Cancel: c | Push: P | Pull: p | Filter: / | Cmd: : | Help: ?",
	"Drop: d | Push: P | Pull: p | Cmd: : | Refresh: R | Help: ?",
}

func (u *tui) options(width int) string {
	if u.pop != nil && u.pop.kind == filterPopup {
		return " Filter:"
	}
	keys := panelKeys[u.m.focus]
	if u.inMain || u.inLog {
		keys = "Scroll: j/k | Page: J/K | Back: esc | Help: ?"
	}
	if u.m.filter != "" {
		keys = "filter: " + u.m.filter + " | " + keys
	}
	left, used := " "+paint(blue, keys), 1+len([]rune(keys))
	if u.m.busy {
		status := taskNames[u.m.task] + " " + spinFrames[int(time.Now().UnixMilli()/int64(spinEvery/time.Millisecond))%len(spinFrames)]
		left = " " + paint(cyan, status) + " " + paint(blue, keys)
		used += len([]rune(status)) + 1
	}
	tag := strings.TrimSpace(version)
	pad := width - used - len([]rune(tag)) - 1
	if pad < 1 {
		return left
	}
	return left + strings.Repeat(" ", pad) + paint(grey, tag) + " "
}

func wrapLog(lines []string, width int) []string {
	var out []string
	for _, l := range lines {
		code := ""
		switch {
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
	if p := u.pop; p != nil && p.kind == filterPopup && p.id == u.drawn {
		if v, err := g.View(popupView); err == nil {
			m.filter = strings.TrimSpace(v.TextArea.GetContent())
			m.clamp()
		}
	}
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
	spans := [panelCount][2]int{{0, hostsEnd - 1}, {hostsEnd, jobsEnd - 1}, {jobsEnd, rows - 1}}
	for p := range panelCount {
		v, _, err := place(g, panelViews[p], 0, spans[p][0], left-1, spans[p][1])
		if err != nil {
			return err
		}
		v.Title, v.TitleColor = panelTitles[p], gocui.ColorDefault
		if p == m.focus && !u.inMain && !u.inLog {
			v.TitleColor = activeTitle
		}
		lines := m.hostLines()
		switch p {
		case jobsPanel:
			lines = m.jobLines(v.InnerWidth() - 1)
		case snapsPanel:
			lines = m.snapLines()
		}
		showList(v, lines, m.sel[p])
	}
	v, _, err := place(g, mainView, left, 0, w-1, mainEnd-1)
	if err != nil {
		return err
	}
	title, text := m.detail()
	v.Title = "[0]-" + title
	v.Clear()
	fmt.Fprint(v, " "+strings.ReplaceAll(text, "\n", "\n "))
	u.scroll = min(u.scroll, max(0, strings.Count(text, "\n")+1-v.InnerHeight()))
	v.SetOrigin(0, u.scroll)
	v, _, err = place(g, logView, left, mainEnd, w-1, rows-1)
	if err != nil {
		return err
	}
	v.Title = "Log"
	v.Clear()
	wrapped := wrapLog(u.log.tail(logTail), v.InnerWidth()-1)
	u.logUp = min(u.logUp, max(0, len(wrapped)-v.InnerHeight()))
	end := len(wrapped) - u.logUp
	fmt.Fprint(v, " "+strings.Join(wrapped[max(0, end-v.InnerHeight()):end], "\n "))
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
	if u.inMain {
		name = mainView
	}
	if u.inLog {
		name = logView
	}
	if u.pop != nil {
		name = popupView
		if u.pop.focus {
			name = suggestView
			if u.pop.kind == scriptPopup {
				name = previewView
			}
		}
	}
	_, err = g.SetCurrentView(name)
	cur := g.CurrentView()
	g.Cursor = cur != nil && cur.Editable
	return err
}

func (u *tui) drawSuggestions(g *gocui.Gui, p *popup, typed string, x0, y0, width, h int) error {
	p.matches = matching(p.history, strings.TrimSpace(typed))
	p.sel = max(0, min(p.sel, len(p.matches)-1))
	if len(p.matches) == 0 {
		p.focus = false
	}
	v, _, err := place(g, suggestView, x0, y0, x0+width-1, min(y0+suggestHeight-1, h-2))
	if err != nil {
		return err
	}
	v.Title = "Suggestions (press <tab> to focus)"
	showList(v, p.matches, p.sel)
	v.Highlight = p.focus
	_, err = g.SetViewOnTop(suggestView)
	return err
}

func (u *tui) drawScripts(g *gocui.Gui, p *popup, w, h int) error {
	width := min(w-8, scriptWidth)
	total := min(h-6, scriptHeight)
	x0, y0 := (w-width)/2, max(0, (h-total)/2)
	bottom := y0 + total - 1
	v, created, err := place(g, popupView, x0, y0, x0+width-1, y0+2)
	if err != nil {
		return err
	}
	u.drawn = p.id
	v.Title, v.TitleColor = p.title, activeTitle
	if created {
		v.Editable, v.Editor = true, gocui.DefaultEditor
		v.TextArea.TypeString(" ")
		v.RenderTextArea()
	}
	p.matches = fuzzy(p.history, strings.TrimSpace(v.TextArea.GetContent()))
	p.sel = max(0, min(p.sel, len(p.matches)-1))
	split := x0 + max(width*2/5, 20)
	list, _, err := place(g, suggestView, x0, y0+3, split-1, bottom)
	if err != nil {
		return err
	}
	list.Title = "Scripts (press <tab> to read)"
	showList(list, p.matches, p.sel)
	prev, _, err := place(g, previewView, split, y0+3, x0+width-1, bottom)
	if err != nil {
		return err
	}
	prev.Clear()
	prev.Title, prev.TitleColor = "Preview", gocui.ColorDefault
	if p.focus {
		prev.TitleColor = activeTitle
	}
	if p.sel < len(p.matches) {
		path := p.matches[p.sel]
		prev.Title = path
		if p.reading != path {
			p.reading, p.readY, p.readX = path, 0, 0
		}
		text, err := head(filepath.Join(u.m.root, path), previewLines)
		if err != nil {
			text = err.Error()
		}
		fmt.Fprint(prev, " "+strings.ReplaceAll(previewText(text), "\n", "\n "))
		p.readY = min(p.readY, max(0, strings.Count(text, "\n")+1-prev.InnerHeight()))
		p.readX = min(p.readX, max(0, widest(text)+1-prev.InnerWidth()))
		prev.SetOrigin(p.readX, p.readY)
	}
	for _, name := range []string{suggestView, previewView, popupView} {
		if _, err := g.SetViewOnTop(name); err != nil {
			return err
		}
	}
	return nil
}

func (u *tui) drawFilter(g *gocui.Gui, p *popup, w, h int) error {
	v, created, err := place(g, popupView, 8, h-2, w, h)
	if err != nil {
		return err
	}
	u.drawn = p.id
	v.Frame = false
	if created {
		v.Editable, v.Editor = true, gocui.DefaultEditor
		v.TextArea.TypeString(" " + p.text)
		v.RenderTextArea()
	}
	_, err = g.SetViewOnTop(popupView)
	return err
}

func (u *tui) drawPopup(g *gocui.Gui, w, h int) error {
	p := u.pop
	if p == nil || p.id != u.drawn {
		for _, name := range []string{popupView, suggestView, previewView} {
			if err := g.DeleteView(name); err != nil && !unknownView(err) {
				return err
			}
		}
		u.drawn = 0
	}
	if p == nil {
		return nil
	}
	switch p.kind {
	case filterPopup:
		return u.drawFilter(g, p, w, h)
	case scriptPopup:
		return u.drawScripts(g, p, w, h)
	}
	width, height := min(w-4, 64), 3
	y0 := max(0, (h-height)/2)
	switch p.kind {
	case inputPopup:
		width = min(promptWidth, max(w-6, min(promptWidth, w-2)))
		y0 = max(0, (h-height-suggestHeight)/2)
	case helpPopup:
		height = strings.Count(p.text, "\n") + 2
	case confirmPopup:
		width = min(w-4, max(len(p.text)+9, 30))
	}
	x0 := (w - width) / 2
	v, created, err := place(g, popupView, x0, y0, x0+width-1, y0+height-1)
	if err != nil {
		return err
	}
	u.drawn = p.id
	v.Title, v.TitleColor = p.title, activeTitle
	switch p.kind {
	case confirmPopup:
		v.Clear()
		fmt.Fprint(v, p.text+" [y/N]")
	case inputPopup:
		if created {
			v.Editable, v.Editor = true, gocui.DefaultEditor
			v.TextArea.TypeString(" " + p.text)
			v.RenderTextArea()
		}
		if err := u.drawSuggestions(g, p, v.TextArea.GetContent(), x0, y0+height, width, h); err != nil {
			return err
		}
	case helpPopup:
		v.Clear()
		fmt.Fprint(v, p.text)
	}
	_, err = g.SetViewOnTop(popupView)
	return err
}
