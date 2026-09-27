package normalize

import (
	"testing"
	"time"

	"ts3/internal/domain"
)

func TestRequiredFrameFailuresStayUnavailable(t *testing.T) {
	base := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	cases := []struct{ name, payload, reason string }{
		{"wrong product", `{"type":"snapshot","product_id":"ETH-USD","bids":[["1","1"]],"asks":[["2","1"]]}`, "PRODUCT_MISMATCH"},
		{"missing product", `{"type":"heartbeat","sequence":1,"last_trade_id":1,"time":"2026-09-27T12:00:00Z"}`, "PRODUCT_MISMATCH"},
		{"partial ack", `{"type":"subscriptions","channels":[{"name":"heartbeat","product_ids":["BTC-USD"]}]}`, "SUBSCRIPTION_MISMATCH"},
		{"duplicate ack channel", `{"type":"subscriptions","channels":[{"name":"heartbeat","product_ids":["BTC-USD"]},{"name":"heartbeat","product_ids":["BTC-USD"]}]}`, "SUBSCRIPTION_MISMATCH"},
		{"bad delta tail", `{"type":"l2update","product_id":"BTC-USD","time":"2026-09-27T12:00:00Z","changes":[["buy","1","1"],["sell","oops","2"]]}`, "INVALID_DELTA"},
		{"missing time", `{"type":"l2update","product_id":"BTC-USD","changes":[["buy","1","1"]]}`, "MISSING_EVENT_TIME"},
		{"provider error", `{"type":"error","message":"Failed to subscribe","reason":"level2 now requires authentication"}`, "SOURCE_ERROR"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := domain.RawRecord{SchemaVersion: 1, RunID: "t", Ordinal: 1, SourceID: "fixture", ConnectionEpoch: 1, Kind: domain.WSFrame, ReceiveTime: base, AdmissionTime: base, UsableFromTime: base}
			r.SetPayload([]byte(tc.payload))
			e, err := (&Normalizer{}).Convert(r)
			if err != nil || e == nil || e.Kind != domain.SourceStatus || e.Status == nil || e.Status.State != "error" || e.Status.Reason != tc.reason {
				t.Fatalf("event=%+v err=%v", e, err)
			}
		})
	}
}

func TestSnapshotTimeUnknownAndLateDeltaAvailability(t *testing.T) {
	base := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		payload  string
		wantKind domain.EventKind
		wantTime bool
	}{
		{`{"type":"snapshot","product_id":"BTC-USD","bids":[["100","1"]],"asks":[["101","1"]]}`, domain.BookSnapshot, false},
		{`{"type":"l2update","product_id":"BTC-USD","time":"2026-09-27T11:59:00Z","changes":[["buy","100.5","1"]]}`, domain.BookDelta, true},
	} {
		r := domain.RawRecord{SchemaVersion: 1, RunID: "t", Ordinal: 1, SourceID: "fixture", ConnectionEpoch: 1, Kind: domain.WSFrame, ReceiveTime: base, AdmissionTime: base.Add(time.Second), UsableFromTime: base.Add(time.Second)}
		r.SetPayload([]byte(tc.payload))
		e, err := (&Normalizer{}).Convert(r)
		if err != nil || e == nil || e.Kind != tc.wantKind || (e.EventTime != nil) != tc.wantTime || e.UsableFromTime != base.Add(time.Second) {
			t.Fatalf("event=%+v err=%v", e, err)
		}
	}
}
