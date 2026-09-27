package record

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"ts3/internal/domain"
	"ts3/internal/durable"
)

const magic = "TS3RAW1\n"
const MaxFrame = 64 << 20

var runIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

var crcTable = crc32.MakeTable(crc32.Castagnoli)

type frameHeader struct {
	RecordType   string            `json:"record_type"`
	Record       *domain.RawRecord `json:"record,omitempty"`
	LastOrdinal  uint64            `json:"last_ordinal,string,omitempty"`
	PrefixSHA256 string            `json:"prefix_sha256,omitempty"`
}

func makeFrame(h frameHeader, payload []byte) ([]byte, error) {
	hb, err := domain.CanonicalJSON(h)
	if err != nil {
		return nil, err
	}
	if len(hb)+len(payload)+8 > MaxFrame {
		return nil, errors.New("raw frame exceeds maximum")
	}
	bodyLen := uint32(4 + len(hb) + len(payload) + 4)
	b := make([]byte, 4+bodyLen)
	binary.BigEndian.PutUint32(b[:4], bodyLen)
	binary.BigEndian.PutUint32(b[4:8], uint32(len(hb)))
	copy(b[8:], hb)
	copy(b[8+len(hb):], payload)
	crc := crc32.Checksum(b[4:len(b)-4], crcTable)
	binary.BigEndian.PutUint32(b[len(b)-4:], crc)
	return b, nil
}

func parseFrame(r io.Reader) (frameHeader, []byte, []byte, error) {
	var h frameHeader
	var length [4]byte
	n, err := io.ReadFull(r, length[:])
	if err == io.EOF && n == 0 {
		return h, nil, nil, io.EOF
	}
	if err != nil {
		return h, nil, nil, fmt.Errorf("truncated frame length: %w", err)
	}
	sz := binary.BigEndian.Uint32(length[:])
	if sz < 8 || sz > MaxFrame {
		return h, nil, nil, errors.New("invalid raw frame length")
	}
	body := make([]byte, sz)
	if _, err = io.ReadFull(r, body); err != nil {
		return h, nil, nil, fmt.Errorf("truncated frame body: %w", err)
	}
	want := binary.BigEndian.Uint32(body[len(body)-4:])
	if crc32.Checksum(body[:len(body)-4], crcTable) != want {
		return h, nil, nil, errors.New("raw CRC mismatch")
	}
	hs := binary.BigEndian.Uint32(body[:4])
	if hs > uint32(len(body)-8) {
		return h, nil, nil, errors.New("invalid header length")
	}
	if err = json.Unmarshal(body[4:4+hs], &h); err != nil {
		return h, nil, nil, fmt.Errorf("invalid frame header: %w", err)
	}
	frame := append(length[:0:0], length[:]...)
	frame = append(frame, body...)
	return h, bytes.Clone(body[4+hs : len(body)-4]), frame, nil
}

type Segment struct {
	Name         string `json:"name"`
	SHA256       string `json:"sha256"`
	FirstOrdinal uint64 `json:"first_ordinal"`
	LastOrdinal  uint64 `json:"last_ordinal"`
}
type Manifest struct {
	RunID            string     `json:"run_id"`
	SchemaVersion    int        `json:"schema_version"`
	Segments         []Segment  `json:"segments"`
	Clean            bool       `json:"clean"`
	CommittedOrdinal uint64     `json:"committed_ordinal"`
	CreatedAt        time.Time  `json:"created_at"`
	Provenance       Provenance `json:"provenance"`
}

// Provenance is operational run metadata, never an analytical replay input.
type Provenance struct {
	SourceEndpoint   string    `json:"source_endpoint,omitempty"`
	ProductID        string    `json:"product_id,omitempty"`
	Channels         []string  `json:"channels,omitempty"`
	StartedAt        time.Time `json:"started_at,omitempty"`
	EndedAt          time.Time `json:"ended_at,omitempty"`
	CodeRevision     string    `json:"code_revision,omitempty"`
	GoVersion        string    `json:"go_version,omitempty"`
	ConfigSHA256     string    `json:"config_sha256,omitempty"`
	MetadataOrdinal  uint64    `json:"metadata_ordinal,string,omitempty"`
	AppliedOrdinal   uint64    `json:"applied_ordinal,string,omitempty"`
	PublishedOrdinal uint64    `json:"published_ordinal,string,omitempty"`
}

type Writer struct {
	dir, runID      string
	f               *os.File
	index           int
	segmentStart    time.Time
	segmentSize     int64
	segmentFirst    uint64
	last            uint64
	prefix          hash.Hash
	segmentHash     hash.Hash
	segments        []Segment
	provenance      Provenance
	closed          bool
	lastDataFsync   time.Duration
	lastMarkerFsync time.Duration
	syncFn          func(*os.File) error
}

func NewWriter(dir, runID string) (*Writer, error) {
	if dir == "" || !runIDPattern.MatchString(runID) {
		return nil, errors.New("invalid recorder path or run ID")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	if err := durable.SyncDir(filepath.Dir(dir)); err != nil {
		return nil, err
	}
	w := &Writer{dir: dir, runID: runID, prefix: sha256.New(), syncFn: func(f *os.File) error { return f.Sync() }}
	if err := w.openSegment(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *Writer) openSegment() error {
	w.index++
	p := filepath.Join(w.dir, fmt.Sprintf("%s-%06d.raw", w.runID, w.index))
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	if _, err = f.Write([]byte(magic)); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = durable.SyncDir(w.dir); err != nil {
		f.Close()
		return err
	}
	w.f = f
	w.segmentHash = sha256.New()
	_, _ = w.segmentHash.Write([]byte(magic))
	w.segmentStart = time.Now().UTC()
	w.segmentSize = int64(len(magic))
	w.segmentFirst = w.last + 1
	return nil
}

func (w *Writer) AppendBatch(records []domain.RawRecord) error {
	if w.closed {
		return errors.New("writer closed")
	}
	if len(records) == 0 {
		return nil
	}
	frames := make([][]byte, 0, len(records))
	for i := range records {
		r := records[i]
		if r.Ordinal != w.last+uint64(i)+1 {
			return fmt.Errorf("ordinal gap: got %d want %d", r.Ordinal, w.last+uint64(i)+1)
		}
		if r.RunID != w.runID {
			return errors.New("run mismatch")
		}
		if err := r.Validate(); err != nil {
			return err
		}
		frame, err := makeFrame(frameHeader{RecordType: "event", Record: &r}, r.Payload)
		if err != nil {
			return err
		}
		frames = append(frames, frame)
	}
	for _, frame := range frames {
		if _, err := w.f.Write(frame); err != nil {
			return err
		}
		_, _ = w.segmentHash.Write(frame)
		w.segmentSize += int64(len(frame))
	}
	fsyncBegan := time.Now()
	if err := w.syncFn(w.f); err != nil {
		return err
	}
	w.lastDataFsync = time.Since(fsyncBegan)
	for _, frame := range frames {
		_, _ = w.prefix.Write(frame)
	}
	last := records[len(records)-1].Ordinal
	marker, err := makeFrame(frameHeader{RecordType: "commit", LastOrdinal: last, PrefixSHA256: hex.EncodeToString(w.prefix.Sum(nil))}, nil)
	if err != nil {
		return err
	}
	if _, err = w.f.Write(marker); err != nil {
		return err
	}
	_, _ = w.segmentHash.Write(marker)
	w.segmentSize += int64(len(marker))
	fsyncBegan = time.Now()
	if err = w.syncFn(w.f); err != nil {
		return err
	}
	w.lastMarkerFsync = time.Since(fsyncBegan)
	w.last = last
	if w.segmentSize >= 256<<20 || time.Now().UTC().Hour() != w.segmentStart.Hour() {
		if err = w.finalizeSegment(); err != nil {
			return err
		}
		return w.openSegment()
	}
	return nil
}

func (w *Writer) finalizeSegment() error {
	if w.f == nil {
		return nil
	}
	if err := w.f.Sync(); err != nil {
		return err
	}
	p := w.f.Name()
	if err := w.f.Close(); err != nil {
		return err
	}
	w.f = nil
	w.segments = append(w.segments, Segment{Name: filepath.Base(p), SHA256: hex.EncodeToString(w.segmentHash.Sum(nil)), FirstOrdinal: w.segmentFirst, LastOrdinal: w.last})
	return nil
}

func (w *Writer) Close(clean bool) error {
	if w.closed {
		return nil
	}
	w.closed = true
	if err := w.finalizeSegment(); err != nil {
		return err
	}
	m := Manifest{RunID: w.runID, SchemaVersion: domain.SchemaVersion, Segments: w.segments, Clean: clean, CommittedOrdinal: w.last, CreatedAt: time.Now().UTC(), Provenance: w.provenance}
	b, err := domain.CanonicalJSON(m)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	p := filepath.Join(w.dir, w.runID+".manifest.json")
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return durable.SyncDir(w.dir)
}

func (w *Writer) CommittedOrdinal() uint64   { return w.last }
func (w *Writer) SetProvenance(p Provenance) { w.provenance = p }
func (w *Writer) LastFsyncDurations() (time.Duration, time.Duration) {
	return w.lastDataFsync, w.lastMarkerFsync
}

type Report struct {
	ValidOrdinal      uint64 `json:"valid_ordinal"`
	CommittedOrdinal  uint64 `json:"committed_ordinal"`
	UncommittedEvents int    `json:"uncommitted_events"`
	Incomplete        bool   `json:"incomplete"`
	Error             string `json:"error,omitempty"`
}
type Reader struct {
	runID    string
	paths    []string
	index    int
	f        *os.File
	prefix   hash.Hash
	pending  []domain.RawRecord
	ready    []domain.RawRecord
	report   Report
	done     bool
	manifest *Manifest
}

func NewReader(dir, runID string) (*Reader, error) {
	if dir == "" || !runIDPattern.MatchString(runID) {
		return nil, errors.New("invalid recorder path or run ID")
	}
	paths, err := filepath.Glob(filepath.Join(dir, runID+"-*.raw"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return nil, os.ErrNotExist
	}
	r := &Reader{paths: paths, prefix: sha256.New(), runID: runID}
	b, err := os.ReadFile(filepath.Join(dir, runID+".manifest.json"))
	if os.IsNotExist(err) {
		r.report.Incomplete = true
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		r.report.Incomplete = true
		r.report.Error = fmt.Sprintf("invalid manifest: %v", err)
		return r, nil
	}
	if m.RunID != runID || m.SchemaVersion != domain.SchemaVersion || len(m.Segments) != len(paths) {
		return nil, errors.New("manifest segment list mismatch")
	}
	for i, s := range m.Segments {
		if s.Name != filepath.Base(paths[i]) {
			return nil, errors.New("manifest segment name mismatch")
		}
		f, err := os.Open(paths[i])
		if err != nil {
			return nil, err
		}
		h := sha256.New()
		_, copyErr := io.Copy(h, f)
		closeErr := f.Close()
		if copyErr != nil {
			return nil, copyErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if hex.EncodeToString(h.Sum(nil)) != s.SHA256 {
			return nil, errors.New("manifest segment digest mismatch")
		}
	}
	r.manifest = &m
	r.report.Incomplete = !m.Clean
	return r, nil
}

func (r *Reader) openNext() error {
	if r.f != nil {
		r.f.Close()
		r.f = nil
	}
	if r.index >= len(r.paths) {
		return io.EOF
	}
	f, err := os.Open(r.paths[r.index])
	if err != nil {
		return err
	}
	r.index++
	b := make([]byte, len(magic))
	if _, err = io.ReadFull(f, b); err != nil {
		f.Close()
		return err
	}
	if string(b) != magic {
		f.Close()
		return errors.New("invalid raw magic")
	}
	r.f = f
	return nil
}

func (r *Reader) Next() (domain.RawRecord, error) {
	if len(r.ready) > 0 {
		v := r.ready[0]
		r.ready = r.ready[1:]
		return v, nil
	}
	if r.done {
		return domain.RawRecord{}, io.EOF
	}
	for {
		if r.f == nil {
			if err := r.openNext(); err != nil {
				if err == io.EOF {
					r.done = true
					r.report.UncommittedEvents = len(r.pending)
					r.report.Incomplete = r.report.Incomplete || len(r.pending) > 0
					if r.manifest != nil && r.manifest.CommittedOrdinal != r.report.CommittedOrdinal {
						r.report.Error = "manifest committed ordinal mismatch"
						r.report.Incomplete = true
						return domain.RawRecord{}, errors.New(r.report.Error)
					}
					return domain.RawRecord{}, io.EOF
				}
				r.report.Error = err.Error()
				return domain.RawRecord{}, err
			}
		}
		h, p, frame, err := parseFrame(r.f)
		if err == io.EOF {
			r.f.Close()
			r.f = nil
			continue
		}
		if err != nil {
			r.report.Error = err.Error()
			r.report.Incomplete = true
			r.report.UncommittedEvents = len(r.pending)
			if r.report.CommittedOrdinal > 0 && (r.manifest == nil || !r.manifest.Clean) {
				r.done = true
				return domain.RawRecord{}, io.EOF
			}
			return domain.RawRecord{}, err
		}
		switch h.RecordType {
		case "event":
			if h.Record == nil {
				return domain.RawRecord{}, errors.New("missing raw record")
			}
			x := *h.Record
			if x.RunID != r.runID {
				return domain.RawRecord{}, errors.New("raw run mismatch")
			}
			x.SetPayload(p)
			if x.PayloadSHA256 != h.Record.PayloadSHA256 {
				return domain.RawRecord{}, errors.New("payload digest mismatch")
			}
			if err = x.Validate(); err != nil {
				return domain.RawRecord{}, err
			}
			if x.Ordinal != r.report.ValidOrdinal+1 {
				return domain.RawRecord{}, fmt.Errorf("raw ordinal gap at %d", x.Ordinal)
			}
			r.report.ValidOrdinal = x.Ordinal
			r.pending = append(r.pending, x)
			_, _ = r.prefix.Write(frame)
		case "commit":
			if len(p) != 0 || h.LastOrdinal != r.report.ValidOrdinal || h.PrefixSHA256 != hex.EncodeToString(r.prefix.Sum(nil)) {
				return domain.RawRecord{}, errors.New("invalid commit marker")
			}
			r.report.CommittedOrdinal = h.LastOrdinal
			r.ready = r.pending
			r.pending = nil
			if len(r.ready) > 0 {
				return r.Next()
			}
		default:
			return domain.RawRecord{}, fmt.Errorf("unknown frame type %q", h.RecordType)
		}
	}
}

func (r *Reader) Report() Report { return r.report }
func (r *Reader) Close() error {
	if r.f != nil {
		return r.f.Close()
	}
	return nil
}

// RebuildManifest writes an explicitly unclean index after the original final
// manifest is missing or partial. It preserves a partial original and never
// changes raw bytes or commit authority.
func RebuildManifest(dir, runID string) (Manifest, error) {
	r, err := NewReader(dir, runID)
	if err != nil {
		return Manifest{}, err
	}
	defer r.Close()
	if r.manifest != nil {
		return Manifest{}, errors.New("manifest already exists")
	}
	for _, path := range r.paths {
		info, err := os.Stat(path)
		if err != nil {
			return Manifest{}, err
		}
		if time.Since(info.ModTime()) < 5*time.Second {
			return Manifest{}, errors.New("raw segment may still be in progress")
		}
	}
	for {
		_, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Manifest{}, err
		}
	}
	if r.report.CommittedOrdinal == 0 {
		return Manifest{}, errors.New("no committed prefix")
	}
	m := Manifest{RunID: runID, SchemaVersion: domain.SchemaVersion, Clean: false, CommittedOrdinal: r.report.CommittedOrdinal, CreatedAt: time.Now().UTC()}
	previous := uint64(0)
	for _, path := range r.paths {
		f, err := os.Open(path)
		if err != nil {
			return Manifest{}, err
		}
		if _, err := f.Seek(int64(len(magic)), io.SeekStart); err != nil {
			f.Close()
			return Manifest{}, err
		}
		last := previous
		for {
			h, _, _, e := parseFrame(f)
			if e == io.EOF {
				break
			}
			if e != nil {
				break
			}
			if h.RecordType == "commit" {
				last = h.LastOrdinal
			}
		}
		if err := f.Close(); err != nil {
			return Manifest{}, err
		}
		f, err = os.Open(path)
		if err != nil {
			return Manifest{}, err
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, f)
		closeErr := f.Close()
		if copyErr != nil {
			return Manifest{}, copyErr
		}
		if closeErr != nil {
			return Manifest{}, closeErr
		}
		m.Segments = append(m.Segments, Segment{Name: filepath.Base(path), SHA256: hex.EncodeToString(hash.Sum(nil)), FirstOrdinal: previous + 1, LastOrdinal: last})
		previous = last
	}
	if previous != m.CommittedOrdinal {
		return Manifest{}, errors.New("segment commit mismatch")
	}
	b, err := domain.CanonicalJSON(m)
	if err != nil {
		return Manifest{}, err
	}
	b = append(b, '\n')
	p := filepath.Join(dir, runID+".manifest.json")
	if existing, err := os.ReadFile(p); err == nil {
		if json.Valid(existing) {
			return Manifest{}, errors.New("manifest already exists")
		}
		info, err := os.Stat(p)
		if err != nil {
			return Manifest{}, err
		}
		if time.Since(info.ModTime()) < 5*time.Second {
			return Manifest{}, errors.New("manifest may still be in progress")
		}
		preserved := p + ".invalid.json"
		if _, err := os.Stat(preserved); err == nil {
			return Manifest{}, errors.New("invalid manifest preservation path already exists")
		} else if !os.IsNotExist(err) {
			return Manifest{}, err
		}
		if err := os.Rename(p, preserved); err != nil {
			return Manifest{}, err
		}
	} else if !os.IsNotExist(err) {
		return Manifest{}, err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return Manifest{}, err
	}
	if _, err = f.Write(b); err != nil {
		f.Close()
		return Manifest{}, err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return Manifest{}, err
	}
	if err = f.Close(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}
