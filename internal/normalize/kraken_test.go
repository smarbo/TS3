package normalize

import (
	"testing"
	"time"
	"ts3/internal/domain"
)

func TestKrakenIdentityAndTimestampRules(t *testing.T) {
	base := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	n := &Normalizer{}
	ordinal := uint64(0)
	convert := func(kind domain.RawKind, p string) *domain.Event {
		ordinal++
		r := domain.RawRecord{SchemaVersion: 1, RunID: "k", Ordinal: ordinal, SourceID: "kraken", ConnectionEpoch: 1, Kind: kind, ReceiveTime: base, AdmissionTime: base, UsableFromTime: base}
		r.SetPayload([]byte(p))
		e, err := n.Convert(r)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	ack := convert(domain.WSFrame, `{"method":"subscribe","req_id":1,"result":{"channel":"book","depth":100,"snapshot":true,"symbol":"BTC/USD"},"success":true}`)
	if ack.Kind != domain.SourceStatus || ack.Status.State != "subscribed" {
		t.Fatal(ack)
	}
	bad := convert(domain.WSFrame, `{"method":"subscribe","req_id":1,"result":{"channel":"book","depth":10,"snapshot":true,"symbol":"BTC/USD"},"success":true}`)
	if bad.Status == nil || bad.Status.Reason != "SUBSCRIPTION_MISMATCH" {
		t.Fatal(bad)
	}
	heartbeat := convert(domain.WSFrame, `{"channel":"heartbeat"}`)
	if heartbeat.Kind != domain.Heartbeat || heartbeat.EventTime != nil {
		t.Fatal(heartbeat)
	}
	wrong := convert(domain.WSFrame, `{"channel":"book","type":"update","data":[{"symbol":"ETH/USD","bids":[{"price":100.0,"qty":1.00000000}],"asks":[],"checksum":1,"timestamp":"2026-09-27T12:00:00Z"}]}`)
	if wrong.Status == nil || wrong.Status.Reason != "PRODUCT_MISMATCH" {
		t.Fatal(wrong)
	}
	good := convert(domain.WSFrame, `{"channel":"book","type":"update","data":[{"symbol":"BTC/USD","bids":[{"price":100.0,"qty":1.00000000}],"asks":[],"checksum":1,"timestamp":"2026-09-27T11:59:00Z"}]}`)
	if good.Kind != domain.BookDelta || good.Delta.Changes[0].SourcePrice != "100.0" || good.Delta.Changes[0].SourceSize != "1.00000000" || good.EventTime == nil || !good.EventTime.Before(good.UsableFromTime) {
		t.Fatal(good)
	}
}

func TestKrakenControlEventsRetainVenue(t *testing.T) {
	base := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		source string
		kind   domain.RawKind
	}{{"kraken-clock", domain.ClockTick}, {"kraken-collector", domain.CaptureGap}, {"kraken-collector", domain.Shutdown}, {"kraken-watchdog", domain.WatchdogIncident}} {
		r := domain.RawRecord{SchemaVersion: 1, RunID: "k", Ordinal: 1, SourceID: tc.source, Kind: tc.kind, ReceiveTime: base, AdmissionTime: base, UsableFromTime: base}
		r.SetPayload(nil)
		e, err := (&Normalizer{}).Convert(r)
		if err != nil || e == nil || e.Venue != "kraken-spot" || e.Instrument != "kraken-spot:BTC/USD" {
			t.Fatalf("event=%+v err=%v", e, err)
		}
	}
}

func TestKrakenSubscriptionIdentityVariants(t *testing.T) {
	base := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		payload string
	}{
		{"missing success", `{"method":"subscribe","req_id":1,"result":{"channel":"book","depth":100,"snapshot":true,"symbol":"BTC/USD"}}`},
		{"wrong request", `{"method":"subscribe","req_id":2,"result":{"channel":"book","depth":100,"snapshot":true,"symbol":"BTC/USD"},"success":true}`},
		{"wrong symbol", `{"method":"subscribe","req_id":1,"result":{"channel":"book","depth":100,"snapshot":true,"symbol":"ETH/USD"},"success":true}`},
		{"missing snapshot", `{"method":"subscribe","req_id":1,"result":{"channel":"book","depth":100,"symbol":"BTC/USD"},"success":true}`},
		{"wrong channel", `{"method":"subscribe","req_id":1,"result":{"channel":"trades","depth":100,"snapshot":true,"symbol":"BTC/USD"},"success":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := domain.RawRecord{SchemaVersion: 1, RunID: "k", Ordinal: 1, SourceID: "kraken", ConnectionEpoch: 1, Kind: domain.WSFrame, ReceiveTime: base, AdmissionTime: base, UsableFromTime: base}
			r.SetPayload([]byte(tc.payload))
			e, err := (&Normalizer{}).Convert(r)
			if err != nil || e == nil || e.Kind != domain.SourceStatus || e.Status.Reason != "SUBSCRIPTION_MISMATCH" {
				t.Fatalf("event=%+v err=%v", e, err)
			}
		})
	}
}

func TestUnknownKrakenMessagesRemainRaw(t *testing.T) {
	base := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	for _, payload := range []string{
		`{"channel":"status","type":"update","data":[{"system":"online"}]}`,
		`{"channel":"new_public_channel","type":"update","data":[]}`,
		`{"channel":"book","type":"future_message","data":[]}`,
	} {
		r := domain.RawRecord{SchemaVersion: 1, RunID: "k", Ordinal: 1, SourceID: "kraken", ConnectionEpoch: 1, Kind: domain.WSFrame, ReceiveTime: base, AdmissionTime: base, UsableFromTime: base}
		r.SetPayload([]byte(payload))
		e, err := (&Normalizer{}).Convert(r)
		if err != nil || e != nil {
			t.Fatalf("unexpected canonical event for %s: %+v, %v", payload, e, err)
		}
	}
}
