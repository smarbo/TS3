package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"ts3/internal/record"
)

func TestPriorIncompleteRunRequiresGap(t *testing.T) {
	dir := t.TempDir()
	if reason, err := priorGap(dir); err != nil || reason != "" {
		t.Fatal(reason, err)
	}
	w, err := record.NewWriter(dir, "clean")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(true); err != nil {
		t.Fatal(err)
	}
	if reason, err := priorGap(dir); err != nil || reason != "" {
		t.Fatal(reason, err)
	}
	w, err = record.NewWriter(dir, "unclean")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(false); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Second)
	if err := os.Chtimes(filepath.Join(dir, "unclean-000001.raw"), later, later); err != nil {
		t.Fatal(err)
	}
	if reason, err := priorGap(dir); err != nil || !strings.Contains(reason, "unclean") {
		t.Fatal(reason, err)
	}
	if err := os.Remove(filepath.Join(dir, "unclean.manifest.json")); err != nil {
		t.Fatal(err)
	}
	if reason, err := priorGap(dir); err != nil || !strings.Contains(reason, "unclean") {
		t.Fatal(reason, err)
	}
}
