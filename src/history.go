package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const historyKept = 100

func historyPath(root string) string {
	return filepath.Join(stateDir(root), "history")
}

// readHistory returns the commands typed in the UI, the newest first and without repeats.
func readHistory(root string) ([]string, error) {
	data, err := os.ReadFile(historyPath(root))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read history: %w", err)
	}
	var seen []string
	for _, line := range slices.Backward(strings.Split(string(data), "\n")) {
		if line != "" && !slices.Contains(seen, line) {
			seen = append(seen, line)
		}
	}
	return seen[:min(len(seen), historyKept)], nil
}

func addHistory(root, command string) error {
	return appendLine(root, historyPath(root), []byte(command))
}

func matching(history []string, typed string) []string {
	var found []string
	needle := strings.ToLower(typed)
	for _, c := range history {
		if strings.Contains(strings.ToLower(c), needle) {
			found = append(found, c)
		}
	}
	return found
}
