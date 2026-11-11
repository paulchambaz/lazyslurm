package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func quoteAll(paths []string) string {
	quoted := make([]string, len(paths))
	for i, p := range paths {
		quoted[i] = quote(p)
	}
	return strings.Join(quoted, " ")
}

func excluded(path string, patterns []string) bool {
	for part := range strings.SplitSeq(path, "/") {
		if part == ".git" {
			return true
		}
		for _, p := range patterns {
			if ok, err := filepath.Match(p, part); err == nil && ok {
				return true
			}
		}
	}
	return false
}

// fileSet lists the files under the push paths that git tracks.
func fileSet(root string, cfg Config) ([]string, error) {
	args := append([]string{"ls-files", "-z", "--cached", "--"}, cfg.Push...)
	c := command("git", args...)
	c.Dir = root
	out, err := c.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	var files []string
	for name := range strings.SplitSeq(string(out), "\x00") {
		if name == "" || excluded(name, cfg.Exclude) {
			continue
		}
		info, err := os.Lstat(filepath.Join(root, name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", name, err)
		}
		if !info.IsDir() {
			files = append(files, name)
		}
	}
	slices.Sort(files)
	return files, nil
}

func nulJoin(names []string) *bytes.Buffer {
	var b bytes.Buffer
	for _, n := range names {
		b.WriteString(n)
		b.WriteByte(0)
	}
	return &b
}

func prepareScript(cfg Config, dir, tree string) string {
	work, keep := dir+"/"+tree, dir+"/keep"
	var b strings.Builder
	fmt.Fprintf(&b, "set -e\nmkdir -p %s\n", quote(work))
	for _, k := range cfg.Keep {
		link := work + "/" + k
		fmt.Fprintf(&b, "mkdir -p %s %s\n", quote(keep+"/"+k), quote(filepath.Dir(link)))
		fmt.Fprintf(&b, "if [ -e %[1]s ] && [ ! -L %[1]s ]; then echo %[2]s >&2; exit 1; fi\n",
			quote(link), quote(k+" is a real file or folder in work/, not a keep symlink"))
		fmt.Fprintf(&b, "ln -sfn %s %s\n", quote(keep+"/"+k), quote(link))
	}
	return b.String()
}

func rsync(stdin io.Reader, args ...string) error {
	c := command("rsync", append([]string{"-e", "ssh -o BatchMode=yes"}, args...)...)
	var errOut bytes.Buffer
	c.Stdin, c.Stderr = stdin, &errOut
	if err := c.Run(); err != nil {
		return fmt.Errorf("rsync: %w: %s", err, strings.TrimSpace(errOut.String()))
	}
	return nil
}

func send(root string, t Target, dir string, files []string) error {
	return rsync(nulJoin(files), "-a", "-0", "--files-from=-", root+"/", t.path(dir+"/work/"))
}

func headCommit(root string) (string, error) {
	c := command("git", "rev-parse", "--verify", "HEAD")
	c.Dir = root
	out, err := c.Output()
	if err != nil {
		return "", errors.New("no commits yet")
	}
	return strings.TrimSpace(string(out)), nil
}

func dirty(root string, cfg Config) (bool, error) {
	c := command("git", append([]string{"status", "--porcelain", "--"}, cfg.Push...)...)
	c.Dir = root
	out, err := c.Output()
	if err != nil {
		return false, fmt.Errorf("git status: %w", err)
	}
	return len(out) > 0, nil
}

// requireClean fails when the push paths hold changes or untracked files git does not ignore.
func requireClean(root string, cfg Config) error {
	isDirty, err := dirty(root, cfg)
	if err != nil {
		return err
	}
	if isDirty {
		return errors.New("the tree under push is not clean: commit or stash first")
	}
	return nil
}

func prune(t Target, cfg Config, dir string, files []string) (int, error) {
	work := quote(dir + "/work")
	paths := quoteAll(cfg.Push)
	out, err := t.run(fmt.Sprintf(
		`cd %s && for p in %s; do if [ -e "$p" ]; then find "$p" \( -type f -o -type l \) -print0; fi; done`,
		work, paths), nil)
	if err != nil {
		return 0, fmt.Errorf("list work/: %w", err)
	}
	sent := make(map[string]bool, len(files))
	for _, f := range files {
		sent[f] = true
	}
	var stale []string
	for name := range strings.SplitSeq(string(out), "\x00") {
		if name != "" && !sent[name] && !excluded(name, cfg.Exclude) {
			stale = append(stale, name)
		}
	}
	if len(stale) == 0 {
		return 0, nil
	}
	_, err = t.run(fmt.Sprintf(
		`cd %s && xargs -0 rm -f -- && for p in %s; do if [ -d "$p" ]; then find "$p" -mindepth 1 -type d -empty -delete; fi; done`,
		work, paths), nulJoin(stale))
	if err != nil {
		return 0, fmt.Errorf("remove stale files: %w", err)
	}
	return len(stale), nil
}

// push makes work/ match the file set and returns the counts sent and removed; the caller holds the lock.
func push(root string, cfg Config, dir string) (sent, removed int, err error) {
	files, err := fileSet(root, cfg)
	if err != nil {
		return 0, 0, err
	}
	if _, err := cfg.Target.run(prepareScript(cfg, dir, "work"), nil); err != nil {
		return 0, 0, fmt.Errorf("prepare work/: %w", err)
	}
	if err := send(root, cfg.Target, dir, files); err != nil {
		return 0, 0, err
	}
	removed, err = prune(cfg.Target, cfg, dir, files)
	if err != nil {
		return 0, 0, err
	}
	return len(files), removed, nil
}

// snapshot hardlinks work/ into dir/commit unless it exists; the caller holds the lock.
func snapshot(t Target, dir, commit string) error {
	snap, tmp := quote(dir+"/"+commit), quote(dir+"/"+commit+".tmp")
	script := fmt.Sprintf("if [ -d %[1]s ]; then exit 0; fi; rm -rf %[2]s && cp -al %[3]s %[2]s && mv %[2]s %[1]s",
		snap, tmp, quote(dir+"/work"))
	if _, err := t.run(script, nil); err != nil {
		return fmt.Errorf("snapshot %s in %s (hardlinks are required): %w", commit, t.Dir, err)
	}
	return nil
}

type synced struct {
	Dir     string
	Commit  string
	Sent    int
	Removed int
}

// syncUp runs pre_push, sends the push set, snapshots HEAD when asked, then runs post_push.
func syncUp(w io.Writer, root string, cfg Config, snap bool) (synced, error) {
	commit, err := headCommit(root)
	if err != nil {
		return synced{}, err
	}
	if err := requireClean(root, cfg); err != nil {
		return synced{}, err
	}
	env := hookEnv(commit, false, cfg.Host)
	if err := localHook(w, "pre_push", root, cfg.PrePush, env); err != nil {
		return synced{}, err
	}
	dir, err := cfg.Target.resolve()
	if err != nil {
		return synced{}, err
	}
	unlock, err := cfg.Target.lock(dir)
	if err != nil {
		return synced{}, err
	}
	s := synced{Dir: dir, Commit: commit}
	s.Sent, s.Removed, err = push(root, cfg, dir)
	if err == nil && snap {
		err = snapshot(cfg.Target, dir, commit)
	}
	if unlockErr := unlock(); err == nil {
		err = unlockErr
	}
	if err != nil {
		return synced{}, err
	}
	hookDir := dir + "/work"
	if snap {
		hookDir = dir + "/" + commit
	}
	if err := targetHook(w, "post_push", cfg.Target, hookDir, cfg.PostPush, env); err != nil {
		return synced{}, err
	}
	return s, nil
}

type pushRow struct {
	Host    string `json:"host"`
	Files   int    `json:"files"`
	Removed int    `json:"removed"`
}

func runPush(w io.Writer, root, hostFlag string, asJSON bool, args []string) error {
	if len(args) > 0 {
		return usageError("push takes no arguments")
	}
	cfg, err := loadConfig(root, hostFlag)
	if err != nil {
		return err
	}
	s, err := syncUp(hookOut, root, cfg, false)
	if err != nil {
		return fmt.Errorf("push to %s: %w", cfg.Host, err)
	}
	if asJSON {
		return json.NewEncoder(w).Encode(pushRow{cfg.Host, s.Sent, s.Removed})
	}
	_, err = fmt.Fprintf(w, "pushed %d files to %s (%d removed)\n", s.Sent, cfg.Host, s.Removed)
	return err
}
