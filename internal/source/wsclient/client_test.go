package wsclient

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPublicWebSocketHandshakeAndFrame(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	done := make(chan error, 1)
	go func() {
		c, err := l.Accept()
		if err != nil {
			done <- err
			return
		}
		defer c.Close()
		br := bufio.NewReader(c)
		req, err := http.ReadRequest(br)
		if err != nil {
			done <- err
			return
		}
		if req.Header.Get("Authorization") != "" {
			done <- fmt.Errorf("unexpected authentication")
			return
		}
		h := sha1.Sum([]byte(req.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		_, err = fmt.Fprintf(c, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", base64.StdEncoding.EncodeToString(h[:]))
		if err != nil {
			done <- err
			return
		}
		// Consume the masked client subscription frame.
		var header [2]byte
		if _, err = io.ReadFull(br, header[:]); err != nil {
			done <- err
			return
		}
		if header[1]&0x80 == 0 {
			done <- fmt.Errorf("unmasked client frame")
			return
		}
		n := int(header[1] & 0x7f)
		if n == 126 {
			var ext [2]byte
			if _, err = io.ReadFull(br, ext[:]); err != nil {
				done <- err
				return
			}
			n = int(ext[0])<<8 | int(ext[1])
		}
		var mask [4]byte
		if _, err = io.ReadFull(br, mask[:]); err != nil {
			done <- err
			return
		}
		p := make([]byte, n)
		if _, err = io.ReadFull(br, p); err != nil {
			done <- err
			return
		}
		for i := range p {
			p[i] ^= mask[i%4]
		}
		if !strings.Contains(string(p), `"channel":"book"`) {
			done <- fmt.Errorf("wrong subscription: %s", p)
			return
		}
		msg := []byte(`{"type":"heartbeat"}`)
		_, err = c.Write(append([]byte{0x81, byte(len(msg))}, msg...))
		done <- err
	}()
	ws, err := Dial(t.Context(), "ws://"+l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	if err = ws.SendText([]byte(`{"method":"subscribe","params":{"channel":"book"}}`)); err != nil {
		t.Fatal(err)
	}
	p, err := ws.Read()
	if err != nil {
		t.Fatal(err)
	}
	if string(p) != `{"type":"heartbeat"}` {
		t.Fatal(string(p))
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

func TestCancellationUnblocksIdleRead(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	serverDone := make(chan error, 1)
	go func() {
		c, err := l.Accept()
		if err != nil {
			serverDone <- err
			return
		}
		defer c.Close()
		br := bufio.NewReader(c)
		req, err := http.ReadRequest(br)
		if err != nil {
			serverDone <- err
			return
		}
		h := sha1.Sum([]byte(req.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		if _, err := fmt.Fprintf(c, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", base64.StdEncoding.EncodeToString(h[:])); err != nil {
			serverDone <- err
			return
		}
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		var b [1]byte
		_, err = c.Read(b[:])
		if err == io.EOF {
			serverDone <- nil
		} else {
			serverDone <- fmt.Errorf("idle connection not closed: %v", err)
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	ws, err := Dial(ctx, "ws://"+l.Addr().String())
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	readDone := make(chan error, 1)
	go func() { _, err := ws.Read(); readDone <- err }()
	cancel()
	select {
	case err := <-readDone:
		if err == nil {
			t.Fatal("idle read succeeded after cancellation")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("idle read blocked after cancellation")
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}
