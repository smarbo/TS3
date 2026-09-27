package progress

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"ts3/internal/domain"
)

type Mark struct {
	Kind        string    `json:"kind"`
	Ordinal     uint64    `json:"ordinal,string"`
	At          time.Time `json:"at"`
	StateSHA256 string    `json:"state_sha256,omitempty"`
}
type Report struct {
	Applied   uint64 `json:"applied"`
	Published uint64 `json:"published"`
}
type Journal struct {
	f      *os.File
	report Report
}

func Open(path string) (*Journal, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	return &Journal{f: f}, nil
}

func (j *Journal) Mark(kind string, ordinal uint64, hash string, at time.Time) error {
	if kind != "applied" && kind != "published" {
		return fmt.Errorf("invalid progress kind %q", kind)
	}
	if kind == "applied" {
		if ordinal <= j.report.Applied {
			return fmt.Errorf("applied order")
		}
	} else {
		if ordinal <= j.report.Published || ordinal > j.report.Applied {
			return fmt.Errorf("published order")
		}
	}
	b, err := domain.CanonicalJSON(Mark{Kind: kind, Ordinal: ordinal, StateSHA256: hash, At: at.UTC()})
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if _, err = j.f.Write(b); err != nil {
		return err
	}
	if err = j.f.Sync(); err != nil {
		return err
	}
	if kind == "applied" {
		j.report.Applied = ordinal
	} else {
		j.report.Published = ordinal
	}
	return nil
}
func (j *Journal) Report() Report { return j.report }
func (j *Journal) Close() error   { return j.f.Close() }

func Recover(path string) (Report, error) {
	f, err := os.Open(path)
	if err != nil {
		return Report{}, err
	}
	defer f.Close()
	var r Report
	s := bufio.NewScanner(f)
	for s.Scan() {
		var m Mark
		if err = json.Unmarshal(s.Bytes(), &m); err != nil {
			return r, err
		}
		switch m.Kind {
		case "applied":
			if m.Ordinal <= r.Applied {
				return r, fmt.Errorf("applied order")
			}
			r.Applied = m.Ordinal
		case "published":
			if m.Ordinal <= r.Published || m.Ordinal > r.Applied {
				return r, fmt.Errorf("published order")
			}
			r.Published = m.Ordinal
		default:
			return r, fmt.Errorf("invalid mark")
		}
	}
	return r, s.Err()
}
