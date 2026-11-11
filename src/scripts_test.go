package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestScriptFilesExpandGlobsNewestFirst(t *testing.T) {
	root := t.TempDir()
	for name, age := range map[string]time.Duration{"slurm/a.sh": 3 * time.Hour, "slurm/b.sh": time.Hour, "slurm/c.sh": 2 * time.Hour} {
		write(t, filepath.Join(root, name), "#!/bin/sh\n")
		when := time.Now().Add(-age)
		if err := os.Chtimes(filepath.Join(root, name), when, when); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(root, "slurm", "notes.txt"), "x\n")
	got, err := scriptFiles(root, []string{"slurm/*.sh", "slurm/a.sh"})
	if err != nil || !slices.Equal(got, []string{"slurm/b.sh", "slurm/c.sh", "slurm/a.sh"}) {
		t.Errorf("scripts = %q, %v", got, err)
	}
}

func TestFuzzyDropsWhatCannotMatchInOrderAndRanksByCloseness(t *testing.T) {
	items := []string{"slurm/go2-recipes-01.sh", "slurm/train.sh", "slurm/prec-x.sh", "slurm/recipes.sh", "slurm/zzz.sh"}
	if got := fuzzy(items, ""); !slices.Equal(got, items) {
		t.Errorf("empty query reordered: %q", got)
	}
	if got := fuzzy(items, "g2r01"); !slices.Equal(got, []string{"slurm/go2-recipes-01.sh"}) {
		t.Errorf("g2r01 = %q", got)
	}
	if got := fuzzy(items, "10"); len(got) != 0 {
		t.Errorf("letters out of order matched: %q", got)
	}
	got := fuzzy(items, "rec")
	if len(got) != 3 || got[0] != "slurm/recipes.sh" {
		t.Errorf("rec = %q, want the word-start match first", got)
	}
	if got := fuzzy(items, "TRAIN"); !slices.Equal(got, []string{"slurm/train.sh"}) {
		t.Errorf("case: %q", got)
	}
	if got := fuzzy([]string{"a/xbxxx.sh", "a/bx.sh"}, "bx"); got[0] != "a/bx.sh" {
		t.Errorf("tight match not first: %q", got)
	}
}
