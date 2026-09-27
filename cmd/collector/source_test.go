package main

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"ts3/internal/book"
	"ts3/internal/domain"
	"ts3/internal/ingress"
	"ts3/internal/normalize"
)

func TestSourceInitializationTimeoutsCloseEpoch(t *testing.T) {
	for _, tc := range []struct {
		name    string
		sendAck bool
		reject  bool
	}{
		{"ack timeout", false, false},
		{"snapshot timeout", true, false},
		{"terminal rejection", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			serverDone := make(chan error, 1)
			go func() {
				c, err := acceptSubscribed(listener)
				if err != nil {
					serverDone <- err
					return
				}
				defer c.Close()
				if tc.sendAck {
					ack := []byte(`{"method":"subscribe","req_id":1,"result":{"channel":"book","depth":100,"snapshot":true,"symbol":"BTC/USD"},"success":true}`)
					if tc.reject {
						ack = []byte(`{"method":"subscribe","req_id":1,"success":false,"error":"public book unavailable"}`)
					}
					if _, err := c.Write(append([]byte{0x81, byte(len(ack))}, ack...)); err != nil {
						serverDone <- err
						return
					}
				}
				_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
				var one [1]byte
				_, err = c.Read(one[:])
				if err != io.EOF {
					serverDone <- fmt.Errorf("socket remained open: %v", err)
					return
				}
				serverDone <- nil
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			var observations []domain.Observation
			emit := func(o domain.Observation, _ bool) error {
				observations = append(observations, o)
				return nil
			}
			fetch := func(context.Context) ([]byte, time.Time, error) {
				return []byte(`{"error":[],"result":{"XXBTZUSD":{"altname":"XBTUSD","status":"online"}}}`), time.Now().UTC(), nil
			}
			err = captureSourceWith(ctx, "ws://"+listener.Addr().String(), emit, make(chan uint64), fetch, 50*time.Millisecond)
			if tc.reject {
				if err == nil || err.Error() != "source rejected subscription: public book unavailable" {
					t.Fatal(err)
				}
			} else if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
			if len(observations) < 4 || observations[0].Kind != domain.MetadataRequest || observations[1].Kind != domain.MetadataResponse || observations[2].Kind != domain.Connected || observations[len(observations)-1].Kind != domain.Disconnected {
				t.Fatalf("unexpected observations: %+v", observations)
			}
			if err := <-serverDone; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func acceptSubscribed(listener net.Listener) (net.Conn, error) {
	c, err := listener.Accept()
	if err != nil {
		return nil, err
	}
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	br := bufio.NewReader(c)
	req, err := http.ReadRequest(br)
	if err == nil {
		h := sha1.Sum([]byte(req.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		_, err = fmt.Fprintf(c, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", base64.StdEncoding.EncodeToString(h[:]))
	}
	if err == nil {
		var header [2]byte
		_, err = io.ReadFull(br, header[:])
		if err == nil {
			if header[0] != 0x81 || header[1]&0x80 == 0 || header[1]&0x7f >= 126 {
				err = fmt.Errorf("invalid client subscription frame")
			} else {
				var mask [4]byte
				_, err = io.ReadFull(br, mask[:])
				if err == nil {
					payload := make([]byte, int(header[1]&0x7f))
					_, err = io.ReadFull(br, payload)
					if err == nil {
						for i := range payload {
							payload[i] ^= mask[i%4]
						}
						want := `{"method":"subscribe","params":{"channel":"book","symbol":["BTC/USD"],"depth":100,"snapshot":true},"req_id":1}`
						if string(payload) != want {
							err = fmt.Errorf("unexpected subscribe: %s", payload)
						}
					}
				}
			}
		}
	}
	if err != nil {
		c.Close()
		return nil, err
	}
	_ = c.SetDeadline(time.Time{})
	return c, nil
}

func sendTextFrame(c net.Conn, payload []byte) error {
	if len(payload) < 126 {
		_, err := c.Write(append([]byte{0x81, byte(len(payload))}, payload...))
		return err
	}
	h := []byte{0x81, 126, 0, 0}
	binary.BigEndian.PutUint16(h[2:], uint16(len(payload)))
	_, err := c.Write(append(h, payload...))
	return err
}

func TestMalformedFrameClosesEpochAndFreshSnapshotRecovers(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverDone := make(chan error, 1)
	go func() {
		for epoch := 1; epoch <= 2; epoch++ {
			c, err := acceptSubscribed(listener)
			if err != nil {
				serverDone <- err
				return
			}
			ack := []byte(`{"method":"subscribe","req_id":1,"result":{"channel":"book","depth":100,"snapshot":true,"symbol":"BTC/USD"},"success":true}`)
			if err = sendTextFrame(c, ack); err == nil {
				if epoch == 1 {
					err = sendTextFrame(c, []byte(`{"channel":"book",`))
				} else {
					checksumInput := []byte("1010020000000010000100000000")
					payload := []byte(fmt.Sprintf(`{"channel":"book","type":"snapshot","data":[{"symbol":"BTC/USD","bids":[{"price":100.00,"qty":1.00000000}],"asks":[{"price":101.00,"qty":2.00000000}],"checksum":%d,"timestamp":"%s"}]}`, crc32.ChecksumIEEE(checksumInput), time.Now().UTC().Format(time.RFC3339Nano)))
					err = sendTextFrame(c, payload)
				}
			}
			if err == nil {
				_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
				var one [1]byte
				_, err = c.Read(one[:])
				if err == io.EOF {
					err = nil
				}
			}
			c.Close()
			if err != nil {
				serverDone <- err
				return
			}
		}
		serverDone <- nil
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	start := time.Now()
	coord := ingress.New("reconnect", start)
	n := &normalize.Normalizer{}
	state := book.New()
	reconnect := make(chan uint64, 2)
	var views []book.View
	var kinds []domain.RawKind
	malformed := false
	emit := func(o domain.Observation, _ bool) error {
		r, err := coord.Accept(o, time.Now().UTC(), time.Since(start))
		if err != nil {
			return err
		}
		kinds = append(kinds, r.Kind)
		e, err := n.Convert(r)
		if err != nil {
			return err
		}
		var v book.View
		if e == nil {
			v, err = state.Skip(r)
		} else {
			v, err = state.Apply(*e)
		}
		if err != nil {
			return err
		}
		views = append(views, v)
		if e != nil && e.Status != nil && e.Status.Reason == "MALFORMED_FRAME" {
			malformed = true
			if v.Health != book.Unhealthy || v.BookSHA256 != "" {
				return fmt.Errorf("malformed frame left usable book: %+v", v)
			}
			reconnect <- r.ConnectionEpoch
		}
		if v.Health == book.Healthy && v.Epoch == 2 {
			cancel()
		}
		return nil
	}
	fetch := func(context.Context) ([]byte, time.Time, error) {
		return []byte(`{"error":[],"result":{"XXBTZUSD":{"altname":"XBTUSD","status":"online"}}}`), time.Now().UTC(), nil
	}
	err = captureSourceWith(ctx, "ws://"+listener.Addr().String(), emit, reconnect, fetch, time.Second)
	if !errors.Is(err, context.Canceled) || !malformed {
		t.Fatalf("capture error=%v malformed=%t", err, malformed)
	}
	seenClose, seenNewConnection, seenHealthy := false, false, false
	for i, kind := range kinds {
		if kind == domain.Disconnected && views[i].Epoch == 1 {
			seenClose = true
		}
		if kind == domain.Connected && views[i].Epoch == 2 {
			seenNewConnection = seenClose
		}
		if views[i].Health == book.Healthy && views[i].Epoch == 2 {
			seenHealthy = seenNewConnection
		}
	}
	if !seenClose || !seenNewConnection || !seenHealthy {
		t.Fatalf("epoch barrier/recovery missing: kinds=%v views=%+v", kinds, views)
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("mock source failed to close")
	}
}
