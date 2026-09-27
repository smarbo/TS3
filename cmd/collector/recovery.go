package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ts3/internal/record"
)

// priorGap inspects the newest previous raw run. A missing or unclean manifest
// requires a new-run gap marker; it never licenses carrying over a book.
func priorGap(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var newest string
	var newestAt time.Time
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".raw") {
			continue
		}
		base := strings.TrimSuffix(entry.Name(), ".raw")
		cut := strings.LastIndexByte(base, '-')
		if cut <= 0 || len(base)-cut-1 != 6 {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return "", err
		}
		if info.ModTime().After(newestAt) {
			newestAt = info.ModTime()
			newest = base[:cut]
		}
	}
	if newest == "" {
		return "", nil
	}
	b, err := os.ReadFile(filepath.Join(dir, newest+".manifest.json"))
	if os.IsNotExist(err) {
		return "UNCLEAN_PRIOR_RUN:" + newest, nil
	}
	if err != nil {
		return "", err
	}
	var m record.Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return "", fmt.Errorf("previous manifest malformed: %w", err)
	}
	if m.RunID != newest || !m.Clean {
		return "UNCLEAN_PRIOR_RUN:" + newest, nil
	}
	return "", nil
}

func awaitStableClock(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	deadline := time.NewTimer(2 * time.Minute)
	defer deadline.Stop()
	lastWall := time.Now().UTC().Round(0)
	anchor := time.Now()
	lastMono := time.Duration(0)
	stable := 0
	for stable < 10 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("clock did not stabilize within two minutes")
		case <-ticker.C:
			wall := time.Now().UTC().Round(0)
			mono := time.Since(anchor)
			diff := wall.Sub(lastWall) - (mono - lastMono)
			if diff < 0 {
				diff = -diff
			}
			if diff > 2*time.Second {
				stable = 0
			} else {
				stable++
			}
			lastWall, lastMono = wall, mono
		}
	}
	return nil
}
