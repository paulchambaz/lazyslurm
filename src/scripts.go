package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// scriptFiles lists the files of the repo that match the scripts globs, the most recently modified first.
func scriptFiles(root string, globs []string) ([]string, error) {
	mtime := map[string]time.Time{}
	for _, g := range globs {
		found, err := filepath.Glob(filepath.Join(root, g))
		if err != nil {
			return nil, fmt.Errorf("scripts glob %q: %w", g, err)
		}
		for _, path := range found {
			info, err := os.Stat(path)
			if err != nil || info.IsDir() {
				continue
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return nil, fmt.Errorf("script %s: %w", path, err)
			}
			mtime[rel] = info.ModTime()
		}
	}
	files := make([]string, 0, len(mtime))
	for rel := range mtime {
		files = append(files, rel)
	}
	slices.SortFunc(files, func(a, b string) int {
		if c := mtime[b].Compare(mtime[a]); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	})
	return files, nil
}

func head(path string, n int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	for len(lines) < n && sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return strings.Join(lines, "\n"), sc.Err()
}

func previewText(text string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		switch {
		case strings.HasPrefix(l, "#SBATCH"):
			lines[i] = paint(cyan, l)
		case strings.HasPrefix(l, "#"):
			lines[i] = paint(grey, l)
		}
	}
	return strings.Join(lines, "\n")
}

func widest(text string) int {
	n := 0
	for line := range strings.SplitSeq(text, "\n") {
		n = max(n, len([]rune(line)))
	}
	return n
}

const (
	fuzzyMatch    = 10
	fuzzyBoundary = 8
	fuzzyRun      = 6
)

func boundary(runes []rune, i int) bool {
	return i == 0 || strings.ContainsRune("/_-. ", runes[i-1])
}

func fuzzyScore(item, query string) (int, bool) {
	s, q := []rune(strings.ToLower(item)), []rune(strings.ToLower(query))
	if len(q) > len(s) {
		return 0, false
	}
	const none = -1 << 30
	prev := make([]int, len(s))
	for i := range s {
		prev[i] = none
		if s[i] == q[0] {
			prev[i] = fuzzyMatch
			if boundary(s, i) {
				prev[i] += fuzzyBoundary
			}
		}
	}
	for _, c := range q[1:] {
		next := make([]int, len(s))
		for i := range s {
			next[i] = none
			if s[i] != c {
				continue
			}
			bonus := fuzzyMatch
			if boundary(s, i) {
				bonus += fuzzyBoundary
			}
			for k := range i {
				if prev[k] == none {
					continue
				}
				score := prev[k] + bonus - (i - k - 1)
				if k == i-1 {
					score += fuzzyRun
				}
				next[i] = max(next[i], score)
			}
		}
		prev = next
	}
	best := none
	for _, score := range prev {
		best = max(best, score)
	}
	return best - len(s)/4, best != none
}

// fuzzy keeps the items that contain the query's letters in order, the closest match first.
func fuzzy(items []string, query string) []string {
	if query == "" {
		return items
	}
	type scored struct {
		item  string
		score int
	}
	var found []scored
	for _, item := range items {
		if score, ok := fuzzyScore(item, query); ok {
			found = append(found, scored{item, score})
		}
	}
	slices.SortStableFunc(found, func(a, b scored) int { return b.score - a.score })
	out := make([]string, len(found))
	for i, f := range found {
		out[i] = f.item
	}
	return out
}
