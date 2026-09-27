package normalize

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"ts3/internal/domain"
)

func krakenMetadata(e *domain.Event, payload []byte, requested time.Time) (*domain.Event, error) {
	var v struct {
		Error  []string `json:"error"`
		Result map[string]struct {
			Altname string `json:"altname"`
			Status  string `json:"status"`
		} `json:"result"`
	}
	if json.Unmarshal(payload, &v) != nil || len(v.Error) != 0 {
		return failure(e, "MALFORMED_METADATA"), nil
	}
	valid := false
	for _, pair := range v.Result {
		if pair.Altname == "XBTUSD" && pair.Status == "online" {
			valid = true
		}
	}
	if !valid {
		return failure(e, "PRODUCT_UNAVAILABLE"), nil
	}
	h := sha256.Sum256(payload)
	e.Kind = domain.ProductMetadata
	e.Metadata = &domain.MetadataData{ProductID: "BTC/USD", Status: "online", RequestTime: requested, ResponseTime: e.ReceiveTime, ResponseSHA256: hex.EncodeToString(h[:])}
	return e, nil
}

type krakenNumber string

func (n *krakenNumber) UnmarshalJSON(b []byte) error {
	s := string(b)
	if strings.HasPrefix(s, `"`) {
		var decoded string
		if err := json.Unmarshal(b, &decoded); err != nil {
			return err
		}
		s = decoded
	}
	*n = krakenNumber(s)
	return nil
}

type krakenLevel struct {
	Price krakenNumber `json:"price"`
	Qty   krakenNumber `json:"qty"`
}
type krakenBookData struct {
	Symbol    string        `json:"symbol"`
	Bids      []krakenLevel `json:"bids"`
	Asks      []krakenLevel `json:"asks"`
	Checksum  json.Number   `json:"checksum"`
	Timestamp string        `json:"timestamp"`
}

func krakenFrame(e *domain.Event, payload []byte) (*domain.Event, error) {
	var h struct {
		Channel string `json:"channel"`
		Type    string `json:"type"`
		Method  string `json:"method"`
		Success *bool  `json:"success"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(payload, &h) != nil {
		return failure(e, "MALFORMED_FRAME"), nil
	}
	if h.Method == "subscribe" {
		var a struct {
			ReqID  int `json:"req_id"`
			Result struct {
				Channel  string `json:"channel"`
				Symbol   string `json:"symbol"`
				Depth    int    `json:"depth"`
				Snapshot bool   `json:"snapshot"`
			} `json:"result"`
		}
		if json.Unmarshal(payload, &a) != nil || h.Success == nil || !*h.Success || a.ReqID != 1 || a.Result.Channel != "book" || a.Result.Symbol != "BTC/USD" || a.Result.Depth != 100 || !a.Result.Snapshot {
			return failure(e, "SUBSCRIPTION_MISMATCH"), nil
		}
		e.Kind = domain.SourceStatus
		e.Status = &domain.StatusData{State: "subscribed"}
		return e, nil
	}
	if h.Error != "" {
		return failure(e, "SOURCE_ERROR"), nil
	}
	if h.Channel == "heartbeat" {
		e.Kind = domain.Heartbeat
		e.Heartbeat = &domain.HeartbeatData{}
		return e, nil
	}
	if h.Channel != "book" {
		return nil, nil
	}
	if h.Type != "snapshot" && h.Type != "update" {
		return nil, nil
	}
	var m struct {
		Data []krakenBookData `json:"data"`
	}
	if json.Unmarshal(payload, &m) != nil || len(m.Data) != 1 {
		return failure(e, "MALFORMED_BOOK_FRAME"), nil
	}
	d := m.Data[0]
	if d.Symbol != "BTC/USD" {
		return failure(e, "PRODUCT_MISMATCH"), nil
	}
	if d.Timestamp == "" {
		return failure(e, "MISSING_EVENT_TIME"), nil
	}
	tm, err := time.Parse(time.RFC3339Nano, d.Timestamp)
	if err != nil {
		return failure(e, "INVALID_EVENT_TIME"), nil
	}
	tm = tm.UTC()
	e.EventTime = &tm
	if tm.After(e.ReceiveTime.Add(2 * time.Second)) {
		e.QualityFlags = append(e.QualityFlags, "EVENT_TIME_FUTURE")
	}
	checksum := string(d.Checksum)
	if _, err := strconv.ParseUint(checksum, 10, 32); err != nil {
		return failure(e, "INVALID_CHECKSUM"), nil
	}
	if h.Type == "snapshot" {
		if len(d.Bids) == 0 || len(d.Asks) == 0 || len(d.Bids) > 100 || len(d.Asks) > 100 {
			return failure(e, "SNAPSHOT_LEVEL_COUNT"), nil
		}
		b, err := krakenLevels(d.Bids, false)
		if err != nil {
			return failure(e, "INVALID_SNAPSHOT_LEVEL"), nil
		}
		a, err := krakenLevels(d.Asks, false)
		if err != nil {
			return failure(e, "INVALID_SNAPSHOT_LEVEL"), nil
		}
		e.Kind = domain.BookSnapshot
		e.Snapshot = &domain.SnapshotData{Bids: b, Asks: a, Checksum: checksum, Depth: 100}
		return e, nil
	}
	if len(d.Bids)+len(d.Asks) == 0 || len(d.Bids)+len(d.Asks) > 1000 {
		return failure(e, "INVALID_DELTA_COUNT"), nil
	}
	changes := make([]domain.Change, 0, len(d.Bids)+len(d.Asks))
	for _, part := range []struct {
		side   string
		levels []krakenLevel
	}{{"buy", d.Bids}, {"sell", d.Asks}} {
		for _, l := range part.levels {
			p, err := domain.Decimal(string(l.Price), false)
			if err != nil {
				return failure(e, "INVALID_DELTA"), nil
			}
			q, err := domain.Decimal(string(l.Qty), true)
			if err != nil {
				return failure(e, "INVALID_DELTA"), nil
			}
			changes = append(changes, domain.Change{Side: part.side, Price: p, AbsoluteSize: q, SourcePrice: string(l.Price), SourceSize: string(l.Qty)})
		}
	}
	e.Kind = domain.BookDelta
	e.Delta = &domain.DeltaData{Changes: changes, Checksum: checksum, Depth: 100}
	return e, nil
}

func krakenLevels(in []krakenLevel, allowZero bool) ([]domain.Level, error) {
	out := make([]domain.Level, 0, len(in))
	for _, l := range in {
		p, err := domain.Decimal(string(l.Price), false)
		if err != nil {
			return nil, err
		}
		q, err := domain.Decimal(string(l.Qty), allowZero)
		if err != nil {
			return nil, err
		}
		out = append(out, domain.Level{Price: p, Size: q, SourcePrice: string(l.Price), SourceSize: string(l.Qty)})
	}
	return out, nil
}
