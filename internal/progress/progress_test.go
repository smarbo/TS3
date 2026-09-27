package progress

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAcknowledgedPrefixAndInvalidJournal(t *testing.T) {
	p := filepath.Join(t.TempDir(), "progress.jsonl")
	j, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Mark("applied", 2, "abc", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := j.Mark("published", 3, "", time.Now()); err == nil {
		t.Fatal("published beyond applied")
	}
	if err := j.Mark("published", 1, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := Recover(p)
	if err != nil || r.Applied != 2 || r.Published != 1 {
		t.Fatalf("report=%+v err=%v", r, err)
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{bad\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Recover(p); err == nil {
		t.Fatal("corrupt journal accepted")
	}
}
