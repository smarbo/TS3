package normalize

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"ts3/internal/domain"
)

type Normalizer struct{ requestTime time.Time }

func (n *Normalizer) Convert(r domain.RawRecord) (*domain.Event, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	e := &domain.Event{SchemaVersion: domain.SchemaVersion, RunID: r.RunID, Ordinal: r.Ordinal, Venue: "coinbase-exchange", Instrument: "coinbase-exchange:BTC-USD", SourceID: r.SourceID, ConnectionEpoch: r.ConnectionEpoch, ReceiveTime: r.ReceiveTime, AdmissionTime: r.AdmissionTime, UsableFromTime: r.UsableFromTime, RawRef: fmt.Sprintf("%d:%s", r.Ordinal, r.PayloadSHA256)}
	kraken := strings.HasPrefix(r.SourceID, "kraken")
	if kraken {
		e.Venue = "kraken-spot"
		e.Instrument = "kraken-spot:BTC/USD"
	}
	status := func(state, reason string) (*domain.Event, error) {
		e.Kind = domain.SourceStatus
		e.Status = &domain.StatusData{State: state, Reason: reason}
		return e, nil
	}
	switch r.Kind {
	case domain.MetadataRequest:
		n.requestTime = r.ReceiveTime
		return nil, nil
	case domain.MetadataResponse:
		if kraken {
			return krakenMetadata(e, r.Payload, n.requestTime)
		}
		var m struct {
			ID              string `json:"id"`
			Status          string `json:"status"`
			TradingDisabled *bool  `json:"trading_disabled"`
		}
		if err := json.Unmarshal(r.Payload, &m); err != nil {
			return status("error", "MALFORMED_METADATA")
		}
		if m.ID != "BTC-USD" || m.Status != "online" || (m.TradingDisabled != nil && *m.TradingDisabled) {
			return status("error", "PRODUCT_UNAVAILABLE")
		}
		h := sha256.Sum256(r.Payload)
		e.Kind = domain.ProductMetadata
		e.Metadata = &domain.MetadataData{ProductID: m.ID, Status: m.Status, TradingDisabled: m.TradingDisabled, RequestTime: n.requestTime, ResponseTime: r.ReceiveTime, ResponseSHA256: hex.EncodeToString(h[:])}
		return e, nil
	case domain.Connected:
		return status("connected", "")
	case domain.Disconnected:
		return status("disconnected", string(r.Payload))
	case domain.SourceError:
		return status("error", string(r.Payload))
	case domain.CaptureGap:
		return status("capture_gap", string(r.Payload))
	case domain.ClockAnomaly:
		return status("error", "CLOCK_ANOMALY")
	case domain.WatchdogIncident:
		return status("error", "WATCHDOG_INCIDENT")
	case domain.Shutdown:
		return status("shutdown", "")
	case domain.ClockTick:
		e.Kind = domain.Tick
		t := r.UsableFromTime
		e.TickTime = &t
		return e, nil
	case domain.WSFrame:
		if kraken {
			return krakenFrame(e, r.Payload)
		}
		return frame(e, r.Payload)
	default:
		return status("error", "UNKNOWN_RAW_KIND")
	}
}

func frame(e *domain.Event, p []byte) (*domain.Event, error) {
	var h struct {
		Type      string `json:"type"`
		ProductID string `json:"product_id"`
		Time      string `json:"time"`
	}
	if err := json.Unmarshal(p, &h); err != nil {
		return failure(e, "MALFORMED_FRAME"), nil
	}
	switch h.Type {
	case "error":
		return failure(e, "SOURCE_ERROR"), nil
	case "subscriptions":
		var v struct {
			Channels []struct {
				Name       string   `json:"name"`
				ProductIDs []string `json:"product_ids"`
			} `json:"channels"`
		}
		if err := json.Unmarshal(p, &v); err != nil {
			return failure(e, "MALFORMED_ACK"), nil
		}
		if len(v.Channels) != 2 {
			return failure(e, "SUBSCRIPTION_MISMATCH"), nil
		}
		seen := map[string]bool{}
		for _, ch := range v.Channels {
			if len(ch.ProductIDs) != 1 || ch.ProductIDs[0] != "BTC-USD" || seen[ch.Name] || (ch.Name != "level2" && ch.Name != "heartbeat") {
				return failure(e, "SUBSCRIPTION_MISMATCH"), nil
			}
			seen[ch.Name] = true
		}
		if !seen["level2"] || !seen["heartbeat"] {
			return failure(e, "SUBSCRIPTION_MISMATCH"), nil
		}
		e.Kind = domain.SourceStatus
		e.Status = &domain.StatusData{State: "subscribed"}
		return e, nil
	case "snapshot", "l2update", "heartbeat":
		if h.ProductID != "BTC-USD" {
			return failure(e, "PRODUCT_MISMATCH"), nil
		}
	default:
		return nil, nil
	}
	if h.Type != "snapshot" {
		if h.Time == "" {
			return failure(e, "MISSING_EVENT_TIME"), nil
		}
		t, err := time.Parse(time.RFC3339Nano, h.Time)
		if err != nil {
			return failure(e, "INVALID_EVENT_TIME"), nil
		}
		t = t.UTC()
		e.EventTime = &t
		if t.After(e.ReceiveTime.Add(2 * time.Second)) {
			e.QualityFlags = append(e.QualityFlags, "EVENT_TIME_FUTURE")
		}
	}
	switch h.Type {
	case "snapshot":
		var v struct {
			Bids [][]string `json:"bids"`
			Asks [][]string `json:"asks"`
		}
		if err := json.Unmarshal(p, &v); err != nil {
			return failure(e, "MALFORMED_SNAPSHOT"), nil
		}
		if len(v.Bids) == 0 || len(v.Asks) == 0 || len(v.Bids) > 250000 || len(v.Asks) > 250000 {
			return failure(e, "SNAPSHOT_LEVEL_COUNT"), nil
		}
		b, err := levels(v.Bids)
		if err != nil {
			return failure(e, "INVALID_SNAPSHOT_LEVEL"), nil
		}
		a, err := levels(v.Asks)
		if err != nil {
			return failure(e, "INVALID_SNAPSHOT_LEVEL"), nil
		}
		e.Kind = domain.BookSnapshot
		e.Snapshot = &domain.SnapshotData{Bids: b, Asks: a}
		return e, nil
	case "l2update":
		var v struct {
			Changes [][]string `json:"changes"`
		}
		if err := json.Unmarshal(p, &v); err != nil {
			return failure(e, "MALFORMED_DELTA"), nil
		}
		if len(v.Changes) == 0 || len(v.Changes) > 250000 {
			return failure(e, "INVALID_DELTA_COUNT"), nil
		}
		changes := make([]domain.Change, 0, len(v.Changes))
		for _, c := range v.Changes {
			if len(c) != 3 || (c[0] != "buy" && c[0] != "sell") {
				return failure(e, "INVALID_DELTA"), nil
			}
			price, err := domain.Decimal(c[1], false)
			if err != nil {
				return failure(e, "INVALID_DELTA"), nil
			}
			size, err := domain.Decimal(c[2], true)
			if err != nil {
				return failure(e, "INVALID_DELTA"), nil
			}
			changes = append(changes, domain.Change{Side: c[0], Price: price, AbsoluteSize: size})
		}
		e.Kind = domain.BookDelta
		e.Delta = &domain.DeltaData{Changes: changes}
		return e, nil
	case "heartbeat":
		var v struct {
			Sequence    json.Number `json:"sequence"`
			LastTradeID json.Number `json:"last_trade_id"`
		}
		if err := json.Unmarshal(p, &v); err != nil {
			return failure(e, "MALFORMED_HEARTBEAT"), nil
		}
		if _, err := strconv.ParseUint(string(v.Sequence), 10, 64); err != nil {
			return failure(e, "INVALID_HEARTBEAT"), nil
		}
		if _, err := strconv.ParseUint(string(v.LastTradeID), 10, 64); err != nil {
			return failure(e, "INVALID_HEARTBEAT"), nil
		}
		e.Kind = domain.Heartbeat
		e.Heartbeat = &domain.HeartbeatData{Sequence: string(v.Sequence), LastTradeID: string(v.LastTradeID)}
		return e, nil
	}
	return nil, nil
}

func levels(raw [][]string) ([]domain.Level, error) {
	out := make([]domain.Level, 0, len(raw))
	seen := make(map[string]bool, len(raw))
	for _, v := range raw {
		if len(v) != 2 {
			return nil, fmt.Errorf("bad tuple")
		}
		p, err := domain.Decimal(v[0], false)
		if err != nil {
			return nil, err
		}
		s, err := domain.Decimal(v[1], false)
		if err != nil {
			return nil, err
		}
		if seen[p] {
			return nil, fmt.Errorf("duplicate price")
		}
		seen[p] = true
		out = append(out, domain.Level{Price: p, Size: s})
	}
	return out, nil
}

func failure(e *domain.Event, reason string) *domain.Event {
	e.Kind = domain.SourceStatus
	e.Status = &domain.StatusData{State: "error", Reason: reason}
	return e
}
