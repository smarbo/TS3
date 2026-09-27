package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

const SchemaVersion = 1

type RawKind string

const (
	WSFrame          RawKind = "WS_FRAME"
	Connected        RawKind = "CONNECTED"
	Disconnected     RawKind = "DISCONNECTED"
	ClockTick        RawKind = "CLOCK_TICK"
	MetadataRequest  RawKind = "METADATA_REQUEST"
	MetadataResponse RawKind = "METADATA_RESPONSE"
	SourceError      RawKind = "SOURCE_ERROR"
	ClockAnomaly     RawKind = "CLOCK_ANOMALY"
	WatchdogIncident RawKind = "WATCHDOG_INCIDENT"
	CaptureGap       RawKind = "CAPTURE_GAP"
	Shutdown         RawKind = "SHUTDOWN"
)

type Observation struct {
	SourceID    string
	Epoch       uint64
	Kind        RawKind
	ReceiveTime time.Time
	Payload     []byte
}

type RawRecord struct {
	SchemaVersion      int       `json:"schema_version"`
	RunID              string    `json:"run_id"`
	Ordinal            uint64    `json:"ordinal,string"`
	SourceID           string    `json:"source_id"`
	ConnectionEpoch    uint64    `json:"connection_epoch,string"`
	Kind               RawKind   `json:"kind"`
	ReceiveTime        time.Time `json:"receive_time"`
	AdmissionTime      time.Time `json:"admission_time"`
	UsableFromTime     time.Time `json:"usable_from_time"`
	MonotonicElapsedNS int64     `json:"monotonic_elapsed_ns,string"`
	PayloadSHA256      string    `json:"payload_sha256"`
	Payload            []byte    `json:"-"`
}

func (r *RawRecord) SetPayload(p []byte) {
	r.Payload = bytes.Clone(p)
	h := sha256.Sum256(p)
	r.PayloadSHA256 = hex.EncodeToString(h[:])
}

func (r RawRecord) Validate() error {
	if r.SchemaVersion != SchemaVersion || r.RunID == "" || r.Ordinal == 0 || r.SourceID == "" || r.Kind == "" {
		return errors.New("invalid raw identity")
	}
	if r.ReceiveTime.IsZero() || r.AdmissionTime.IsZero() || r.UsableFromTime.IsZero() {
		return errors.New("missing raw time")
	}
	if r.UsableFromTime.Before(r.AdmissionTime) {
		return errors.New("usable time before admission")
	}
	h := sha256.Sum256(r.Payload)
	if r.PayloadSHA256 != hex.EncodeToString(h[:]) {
		return errors.New("raw payload hash mismatch")
	}
	return nil
}

type EventKind string

const (
	BookSnapshot    EventKind = "BookSnapshot"
	BookDelta       EventKind = "BookDelta"
	Heartbeat       EventKind = "Heartbeat"
	SourceStatus    EventKind = "SourceStatus"
	Tick            EventKind = "ClockTick"
	ProductMetadata EventKind = "ProductMetadata"
)

type Level struct {
	Price       string `json:"price"`
	Size        string `json:"size"`
	SourcePrice string `json:"source_price,omitempty"`
	SourceSize  string `json:"source_size,omitempty"`
}
type Change struct {
	Side         string `json:"side"`
	Price        string `json:"price"`
	AbsoluteSize string `json:"absolute_size"`
	SourcePrice  string `json:"source_price,omitempty"`
	SourceSize   string `json:"source_size,omitempty"`
}
type SnapshotData struct {
	Bids     []Level `json:"bids"`
	Asks     []Level `json:"asks"`
	Checksum string  `json:"checksum,omitempty"`
	Depth    int     `json:"depth,omitempty"`
}
type DeltaData struct {
	Changes  []Change `json:"changes"`
	Checksum string   `json:"checksum,omitempty"`
	Depth    int      `json:"depth,omitempty"`
}
type HeartbeatData struct {
	Sequence    string `json:"sequence"`
	LastTradeID string `json:"last_trade_id"`
}
type StatusData struct {
	State  string `json:"state"`
	Reason string `json:"reason"`
}
type MetadataData struct {
	ProductID       string    `json:"product_id"`
	Status          string    `json:"status"`
	TradingDisabled *bool     `json:"trading_disabled"`
	RequestTime     time.Time `json:"request_time"`
	ResponseTime    time.Time `json:"response_time"`
	ResponseSHA256  string    `json:"response_sha256"`
}

type Event struct {
	SchemaVersion   int            `json:"schema_version"`
	RunID           string         `json:"run_id"`
	Ordinal         uint64         `json:"ordinal,string"`
	Venue           string         `json:"venue"`
	Instrument      string         `json:"instrument"`
	SourceID        string         `json:"source_id"`
	ConnectionEpoch uint64         `json:"connection_epoch,string"`
	Kind            EventKind      `json:"kind"`
	EventTime       *time.Time     `json:"event_time"`
	PublicationTime *time.Time     `json:"publication_time"`
	ReceiveTime     time.Time      `json:"receive_time"`
	AdmissionTime   time.Time      `json:"admission_time"`
	UsableFromTime  time.Time      `json:"usable_from_time"`
	RawRef          string         `json:"raw_ref"`
	QualityFlags    []string       `json:"quality_flags,omitempty"`
	Snapshot        *SnapshotData  `json:"snapshot,omitempty"`
	Delta           *DeltaData     `json:"delta,omitempty"`
	Heartbeat       *HeartbeatData `json:"heartbeat,omitempty"`
	Status          *StatusData    `json:"source_status,omitempty"`
	TickTime        *time.Time     `json:"tick_time,omitempty"`
	Metadata        *MetadataData  `json:"product_metadata,omitempty"`
}

// Decimal returns a unique plain-text representation. Book arithmetic never uses float64.
func Decimal(s string, allowZero bool) (string, error) {
	if s == "" || strings.HasPrefix(s, "-") || strings.HasPrefix(s, "+") || strings.ContainsAny(s, "eE ") {
		return "", fmt.Errorf("invalid decimal %q", s)
	}
	p := strings.Split(s, ".")
	if len(p) > 2 || len(p[0]) == 0 || len(p[0]) > 18 {
		return "", fmt.Errorf("invalid decimal scale %q", s)
	}
	if len(p) == 2 && (len(p[1]) == 0 || len(p[1]) > 18) {
		return "", fmt.Errorf("invalid decimal scale %q", s)
	}
	for _, part := range p {
		for _, c := range part {
			if c < '0' || c > '9' {
				return "", fmt.Errorf("invalid decimal digit %q", s)
			}
		}
	}
	i := strings.TrimLeft(p[0], "0")
	if i == "" {
		i = "0"
	}
	f := ""
	if len(p) == 2 {
		f = strings.TrimRight(p[1], "0")
	}
	if i == "0" && f == "" && !allowZero {
		return "", errors.New("zero decimal")
	}
	if f != "" {
		return i + "." + f, nil
	}
	return i, nil
}

// CompareDecimal compares canonical nonnegative decimals without floating point.
func CompareDecimal(a, b string) int {
	ap, bp := strings.SplitN(a, ".", 2), strings.SplitN(b, ".", 2)
	if len(ap[0]) != len(bp[0]) {
		if len(ap[0]) < len(bp[0]) {
			return -1
		}
		return 1
	}
	if ap[0] != bp[0] {
		if ap[0] < bp[0] {
			return -1
		}
		return 1
	}
	af, bf := "", ""
	if len(ap) > 1 {
		af = ap[1]
	}
	if len(bp) > 1 {
		bf = bp[1]
	}
	n := len(af)
	if len(bf) > n {
		n = len(bf)
	}
	for len(af) < n {
		af += "0"
	}
	for len(bf) < n {
		bf += "0"
	}
	return strings.Compare(af, bf)
}

// CanonicalJSON encodes JSON-compatible typed values with lexicographic keys.
func CanonicalJSON(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var x any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err = d.Decode(&x); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err = writeCanonical(&out, x); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func writeCanonical(b *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		b.WriteString(strconv.FormatBool(x))
	case string:
		z, _ := json.Marshal(x)
		b.Write(z)
	case json.Number:
		b.WriteString(string(x))
	case []any:
		b.WriteByte('[')
		for i, y := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeCanonical(b, y); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			z, _ := json.Marshal(k)
			b.Write(z)
			b.WriteByte(':')
			if err := writeCanonical(b, x[k]); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	default:
		return fmt.Errorf("unsupported canonical JSON type %T", v)
	}
	return nil
}
