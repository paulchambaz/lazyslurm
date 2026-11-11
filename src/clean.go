package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

var jobName = regexp.MustCompile(`^[0-9]+$`)

const scanScript = `cd %s || exit 1; keep=$(pwd -P)
scan() {
[ -d "$1" ] && [ ! -L "$1" ] || return 0
case "$(cd "$1" && pwd -P)/" in "$keep"/*) ;; *) return 0 ;; esac
find "$1" -mindepth "$2" -maxdepth "$2" -type d -printf '%%p\0'
}
%s`

const globChars = "*?["

type cleanItem struct {
	Path string
	KB   int
}

func cleanParent(pattern string) string {
	return strings.TrimSuffix(pattern, "/"+jobToken)
}

func cleanScan(pattern string) (start string, depth int) {
	parts := strings.Split(cleanParent(pattern), "/")
	i := slices.IndexFunc(parts, func(c string) bool { return strings.ContainsAny(c, globChars) })
	if i < 0 {
		i = len(parts)
	}
	return strings.Join(parts[:i], "/"), len(parts) - i + 1
}

func cleanScans(cfg Config) string {
	var b strings.Builder
	for _, p := range cfg.Clean {
		start, depth := cleanScan(p)
		fmt.Fprintf(&b, "scan %s %d\n", quote(start), depth)
	}
	return b.String()
}

func cleanable(cfg Config, path string) bool {
	return jobName.MatchString(filepath.Base(path)) && slices.ContainsFunc(cfg.Clean, func(p string) bool {
		ok, err := filepath.Match(cleanParent(p), filepath.Dir(path))
		return err == nil && ok
	})
}

func rsyncOut(stdin io.Reader, args ...string) (string, error) {
	c := command("rsync", append([]string{"-e", "ssh -o BatchMode=yes"}, args...)...)
	var out, errOut bytes.Buffer
	c.Stdin, c.Stdout, c.Stderr = stdin, &out, &errOut
	if err := c.Run(); err != nil {
		return "", fmt.Errorf("rsync: %w: %s", err, strings.TrimSpace(errOut.String()))
	}
	return out.String(), nil
}

func cleanCandidates(root string, cfg Config, dir string) ([]string, map[string]Job, error) {
	out, err := cfg.Target.run(fmt.Sprintf(scanScript, quote(dir+"/keep"), cleanScans(cfg)), nil)
	if err != nil {
		return nil, nil, fmt.Errorf("list outputs: %w", err)
	}
	journal, err := readJournal(root)
	if err != nil {
		return nil, nil, err
	}
	known := map[string]Job{}
	for _, j := range journal {
		if j.Host == cfg.Host {
			known[j.Job] = j
		}
	}
	jobs := map[string]Job{}
	var paths []string
	for p := range strings.SplitSeq(string(out), "\x00") {
		if j, ok := known[filepath.Base(p)]; ok && cleanable(cfg, p) {
			paths = append(paths, p)
			jobs[j.Job] = j
		}
	}
	slices.Sort(paths)
	return slices.Compact(paths), jobs, nil
}

func cleanFinished(cfg Config, paths []string, jobs map[string]Job) ([]string, error) {
	states, _, err := fetchStates(cfg.Target, slices.Collect(maps.Values(jobs)), time.Now())
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(paths, func(p string) bool { return !states[filepath.Base(p)].Final }), nil
}

func notPulled(out string, paths []string) map[string]bool {
	left := map[string]bool{}
	for line := range strings.SplitSeq(out, "\n") {
		if len(line) < 12 || !strings.ContainsRune("<>c*", rune(line[0])) || !strings.ContainsRune("fdLDS", rune(line[1])) {
			continue
		}
		_, name, _ := strings.Cut(line, " ")
		name = strings.TrimSuffix(strings.TrimSpace(name), "/")
		for _, p := range paths {
			if inside(name, p) {
				left[p] = true
			}
		}
	}
	return left
}

func cleanPulled(root string, cfg Config, dir string, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	out, err := rsyncOut(nulJoin(paths), "-rltn", "--itemize-changes", "-0", "--files-from=-",
		cfg.Target.path(dir+"/keep/"), root+"/")
	if err != nil {
		return nil, err
	}
	left := notPulled(out, paths)
	return slices.DeleteFunc(paths, func(p string) bool { return left[p] }), nil
}

func cleanSizes(cfg Config, dir string, paths []string) ([]cleanItem, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	out, err := cfg.Target.run("cd "+quote(dir+"/keep")+" && du -sk -- "+quoteAll(paths), nil)
	if err != nil {
		return nil, fmt.Errorf("size outputs: %w", err)
	}
	var items []cleanItem
	for line := range strings.SplitSeq(string(out), "\n") {
		size, path, ok := strings.Cut(line, "\t")
		kb, err := strconv.Atoi(size)
		if ok && err == nil && slices.Contains(paths, path) {
			items = append(items, cleanItem{path, kb})
		}
	}
	return items, nil
}

// planClean lists the outputs of finished jobs that are already in the repo, without deleting anything.
func planClean(root string, cfg Config) ([]cleanItem, error) {
	if len(cfg.Clean) == 0 {
		return nil, errors.New("no clean key for this host in " + configFile)
	}
	dir, err := cfg.Target.resolve()
	if err != nil {
		return nil, err
	}
	paths, jobs, err := cleanCandidates(root, cfg, dir)
	if err != nil {
		return nil, err
	}
	if paths, err = cleanFinished(cfg, paths, jobs); err != nil {
		return nil, err
	}
	if paths, err = cleanPulled(root, cfg, dir, paths); err != nil {
		return nil, err
	}
	return cleanSizes(cfg, dir, paths)
}

// deleteClean removes the planned outputs from the target.
func deleteClean(cfg Config, items []cleanItem) error {
	if len(items) == 0 {
		return nil
	}
	dir, err := cfg.Target.resolve()
	if err != nil {
		return err
	}
	paths := make([]string, len(items))
	for i, it := range items {
		if !cleanable(cfg, it.Path) {
			return fmt.Errorf("clean: %q is not an output of a job", it.Path)
		}
		paths[i] = it.Path
	}
	if _, err := cfg.Target.run("cd "+quote(dir+"/keep")+" && rm -rf -- "+quoteAll(paths), nil); err != nil {
		return fmt.Errorf("delete outputs: %w", err)
	}
	return nil
}

func cleanTotal(items []cleanItem) int {
	kb := 0
	for _, it := range items {
		kb += it.KB
	}
	return kb
}

func listClean(w io.Writer, items []cleanItem) error {
	for _, it := range items {
		if _, err := fmt.Fprintf(w, "%s  %s\n", it.Path, human(it.KB)); err != nil {
			return err
		}
	}
	return nil
}

type cleanRow struct {
	Host  string   `json:"host"`
	Paths []string `json:"paths"`
	KB    int      `json:"kb"`
}

func runClean(w io.Writer, root, hostFlag string, ask confirmer, asJSON bool, args []string) error {
	if len(args) > 0 {
		return usageError("clean takes no arguments")
	}
	cfg, err := loadConfig(root, hostFlag)
	if err != nil {
		return err
	}
	items, err := planClean(root, cfg)
	if err != nil {
		return fmt.Errorf("clean %s: %w", cfg.Host, err)
	}
	row := cleanRow{cfg.Host, []string{}, cleanTotal(items)}
	if len(items) > 0 {
		if err := listClean(hookOut, items); err != nil {
			return err
		}
		if err := ask.ask(fmt.Sprintf("would delete %d runs on %s (%s)", len(items), cfg.Host, human(row.KB))); err != nil {
			return err
		}
		if err := deleteClean(cfg, items); err != nil {
			return err
		}
		for _, it := range items {
			row.Paths = append(row.Paths, it.Path)
		}
	}
	if asJSON {
		return json.NewEncoder(w).Encode(row)
	}
	if len(items) == 0 {
		_, err = fmt.Fprintln(w, "nothing to clean")
		return err
	}
	_, err = fmt.Fprintf(w, "cleaned %d runs on %s (%s freed)\n", len(items), cfg.Host, human(row.KB))
	return err
}
