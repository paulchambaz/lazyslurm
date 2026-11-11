package main

import (
	"slices"
	"testing"
)

func TestHistoryIsNewestFirstWithoutRepeats(t *testing.T) {
	root := t.TempDir()
	for _, c := range []string{"ls", "nvidia-smi", "ls", "python -V"} {
		if err := addHistory(root, c); err != nil {
			t.Fatal(err)
		}
	}
	got, err := readHistory(root)
	if err != nil || !slices.Equal(got, []string{"python -V", "ls", "nvidia-smi"}) {
		t.Errorf("history = %q, %v", got, err)
	}
	if got := matching(got, "NV"); !slices.Equal(got, []string{"nvidia-smi"}) {
		t.Errorf("matching = %q", got)
	}
	if got, err := readHistory(t.TempDir()); err != nil || got != nil {
		t.Errorf("empty history = %q, %v", got, err)
	}
}
