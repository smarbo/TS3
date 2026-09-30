package processor

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"os"
	"path/filepath"
	"time"

	"ts3/internal/book"
	"ts3/internal/domain"
	"ts3/internal/durable"
	"ts3/internal/normalize"
	"ts3/internal/progress"
)

type Processor struct {
	n                     *normalize.Normalizer
	s                     *book.State
	normalized, state     *os.File
	normalHash, stateHash hash.Hash
	journal               *progress.Journal
	last                  uint64
	failed                error
}

func New(dir string, live bool) (*Processor, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	if err := durable.SyncDir(filepath.Dir(dir)); err != nil {
		return nil, err
	}
	nf, err := os.OpenFile(filepath.Join(dir, "normalized.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	sf, err := os.OpenFile(filepath.Join(dir, "state.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		nf.Close()
		return nil, err
	}
	p := &Processor{n: &normalize.Normalizer{}, s: book.New(), normalized: nf, state: sf, normalHash: sha256.New(), stateHash: sha256.New()}
	if live {
		p.journal, err = progress.Open(filepath.Join(dir, "progress.jsonl"))
		if err != nil {
			nf.Close()
			sf.Close()
			return nil, err
		}
	}
	if err := durable.SyncDir(dir); err != nil {
		nf.Close()
		sf.Close()
		if p.journal != nil {
			p.journal.Close()
		}
		return nil, err
	}
	return p, nil
}

func (p *Processor) Apply(r domain.RawRecord) (book.View, error) {
	if p.failed != nil {
		return book.View{}, p.failed
	}
	e, err := p.n.Convert(r)
	if err != nil {
		return book.View{}, p.fail(err)
	}
	var v book.View
	if e == nil {
		v, err = p.s.Skip(r)
	} else {
		v, err = p.s.Apply(*e)
	}
	if err != nil {
		return v, p.fail(err)
	}
	if e != nil {
		b, err := domain.CanonicalJSON(e)
		if err != nil {
			return v, p.fail(err)
		}
		b = append(b, '\n')
		if _, err = p.normalized.Write(b); err != nil {
			return v, p.fail(err)
		}
		_, _ = p.normalHash.Write(b)
	}
	b, err := domain.CanonicalJSON(v)
	if err != nil {
		return v, p.fail(err)
	}
	b = append(b, '\n')
	if _, err = p.state.Write(b); err != nil {
		return v, p.fail(err)
	}
	_, _ = p.stateHash.Write(b)
	p.last = r.Ordinal
	return v, nil
}

func (p *Processor) fail(err error) error {
	if p.failed == nil {
		p.failed = err
	}
	return p.failed
}

// Flush acknowledges a contiguous applied batch only after the derived files are durable.
func (p *Processor) Flush() error {
	if p.failed != nil {
		return p.failed
	}
	if err := p.normalized.Sync(); err != nil {
		return p.fail(err)
	}
	if err := p.state.Sync(); err != nil {
		return p.fail(err)
	}
	if p.journal != nil && p.last > p.journal.Report().Applied {
		if err := p.journal.Mark("applied", p.last, p.stateDigest(), time.Now()); err != nil {
			return p.fail(err)
		}
		if err := p.journal.Mark("published", p.last, "", time.Now()); err != nil {
			return p.fail(err)
		}
	}
	return nil
}
func (p *Processor) stateDigest() string { return hex.EncodeToString(p.stateHash.Sum(nil)) }

func (p *Processor) Hashes() (string, string) {
	return hex.EncodeToString(p.normalHash.Sum(nil)), hex.EncodeToString(p.stateHash.Sum(nil))
}
func (p *Processor) Last() uint64      { return p.last }
func (p *Processor) Quote() book.Quote { return p.s.Quote() }
func (p *Processor) Progress() progress.Report {
	if p.journal == nil {
		return progress.Report{}
	}
	return p.journal.Report()
}
func (p *Processor) Close() error {
	var first error
	for _, f := range []*os.File{p.normalized, p.state} {
		if f != nil {
			if err := f.Sync(); err != nil && first == nil {
				first = err
			}
			if err := f.Close(); err != nil && first == nil {
				first = err
			}
		}
	}
	if p.journal != nil {
		if err := p.journal.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}
