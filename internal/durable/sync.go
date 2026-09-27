package durable

import (
	"os"
	"runtime"
)

// SyncDir persists created file or directory names on Unix filesystems.
// Windows does not support syncing a directory through os.File.Sync.
func SyncDir(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	err = d.Sync()
	closeErr := d.Close()
	if err != nil {
		return err
	}
	return closeErr
}
