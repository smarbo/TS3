package replay

import (
	"context"
	"io"
	"time"

	"ts3/internal/domain"
	"ts3/internal/record"
)

type Source struct {
	reader   *record.Reader
	paced    bool
	previous time.Time
}

func Open(dir, runID string, paced bool) (*Source, error) {
	r, err := record.NewReader(dir, runID)
	if err != nil {
		return nil, err
	}
	return &Source{reader: r, paced: paced}, nil
}
func (s *Source) Next(ctx context.Context) (domain.RawRecord, error) {
	select {
	case <-ctx.Done():
		return domain.RawRecord{}, ctx.Err()
	default:
	}
	r, err := s.reader.Next()
	if err != nil {
		return r, err
	}
	if s.paced && !s.previous.IsZero() {
		d := r.UsableFromTime.Sub(s.previous)
		if d > 0 {
			timer := time.NewTimer(d)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return domain.RawRecord{}, ctx.Err()
			case <-timer.C:
			}
		}
	}
	s.previous = r.UsableFromTime
	return r, nil
}
func (s *Source) Report() record.Report { return s.reader.Report() }
func (s *Source) Close() error          { return s.reader.Close() }

var _ io.Closer = (*Source)(nil)
