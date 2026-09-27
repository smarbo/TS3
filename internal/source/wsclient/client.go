package wsclient

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const maxMessage = 64 << 20

type Conn struct {
	c    net.Conn
	r    *bufio.Reader
	mu   sync.Mutex
	stop func() bool
}

func Dial(ctx context.Context, endpoint string) (*Conn, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "wss" && u.Scheme != "ws" {
		return nil, errors.New("WebSocket scheme must be ws or wss")
	}
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		if u.Scheme == "wss" {
			port = "443"
		} else {
			port = "80"
		}
	}
	d := net.Dialer{Timeout: 5 * time.Second}
	var nc net.Conn
	if u.Scheme == "wss" {
		nc, err = tls.DialWithDialer(&d, "tcp", net.JoinHostPort(host, port), &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	} else {
		nc, err = d.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	}
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { nc.Close() })
	keyBytes := make([]byte, 16)
	if _, err = rand.Read(keyBytes); err != nil {
		stop()
		nc.Close()
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)
	path := u.RequestURI()
	if path == "" {
		path = "/"
	}
	req := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n", path, u.Host, key)
	_ = nc.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err = io.WriteString(nc, req); err != nil {
		stop()
		nc.Close()
		return nil, err
	}
	br := bufio.NewReader(nc)
	response, err := http.ReadResponse(br, &http.Request{Method: "GET"})
	if err != nil {
		stop()
		nc.Close()
		return nil, err
	}
	response.Body.Close()
	want := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	if response.StatusCode != 101 || !strings.EqualFold(response.Header.Get("Upgrade"), "websocket") || !strings.Contains(strings.ToLower(response.Header.Get("Connection")), "upgrade") || response.Header.Get("Sec-WebSocket-Accept") != base64.StdEncoding.EncodeToString(want[:]) {
		stop()
		nc.Close()
		return nil, fmt.Errorf("invalid WebSocket upgrade: %s", response.Status)
	}
	_ = nc.SetDeadline(time.Time{})
	return &Conn{c: nc, r: br, stop: stop}, nil
}

func (w *Conn) SendText(p []byte) error { return w.writeFrame(1, p) }
func (w *Conn) Close() error {
	if w.stop != nil {
		w.stop()
	}
	return w.c.Close()
}
func (w *Conn) SetReadDeadline(t time.Time) error { return w.c.SetReadDeadline(t) }

func (w *Conn) writeFrame(op byte, p []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(p) > maxMessage {
		return errors.New("message too large")
	}
	var h bytes.Buffer
	h.WriteByte(0x80 | op)
	if len(p) < 126 {
		h.WriteByte(0x80 | byte(len(p)))
	} else if len(p) <= 65535 {
		h.WriteByte(0x80 | 126)
		var x [2]byte
		binary.BigEndian.PutUint16(x[:], uint16(len(p)))
		h.Write(x[:])
	} else {
		h.WriteByte(0x80 | 127)
		var x [8]byte
		binary.BigEndian.PutUint64(x[:], uint64(len(p)))
		h.Write(x[:])
	}
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return err
	}
	h.Write(mask[:])
	for i, c := range p {
		h.WriteByte(c ^ mask[i%4])
	}
	_, err := w.c.Write(h.Bytes())
	return err
}

func (w *Conn) Read() ([]byte, error) {
	var message bytes.Buffer
	fragmented := false
	for {
		var h [2]byte
		if _, err := io.ReadFull(w.r, h[:]); err != nil {
			return nil, err
		}
		if h[0]&0x70 != 0 {
			return nil, errors.New("unexpected WebSocket extension bits")
		}
		fin := h[0]&0x80 != 0
		op := h[0] & 0x0f
		if h[1]&0x80 != 0 {
			return nil, errors.New("masked server frame")
		}
		n := uint64(h[1] & 0x7f)
		if n == 126 {
			var x [2]byte
			if _, err := io.ReadFull(w.r, x[:]); err != nil {
				return nil, err
			}
			n = uint64(binary.BigEndian.Uint16(x[:]))
		} else if n == 127 {
			var x [8]byte
			if _, err := io.ReadFull(w.r, x[:]); err != nil {
				return nil, err
			}
			n = binary.BigEndian.Uint64(x[:])
		}
		if n > maxMessage || uint64(message.Len())+n > maxMessage {
			return nil, errors.New("WebSocket message too large")
		}
		if op >= 8 && (!fin || n > 125) {
			return nil, errors.New("invalid WebSocket control frame")
		}
		p := make([]byte, int(n))
		if _, err := io.ReadFull(w.r, p); err != nil {
			return nil, err
		}
		switch op {
		case 8:
			return nil, io.EOF
		case 9:
			if err := w.writeFrame(10, p); err != nil {
				return nil, err
			}
			continue
		case 10:
			continue
		case 1:
			if fragmented {
				return nil, errors.New("new text frame within fragment")
			}
			message.Write(p)
			fragmented = !fin
		case 0:
			if !fragmented {
				return nil, errors.New("unexpected continuation")
			}
			message.Write(p)
			fragmented = !fin
		default:
			return nil, fmt.Errorf("unsupported WebSocket opcode %d", op)
		}
		if fin {
			return message.Bytes(), nil
		}
	}
}
