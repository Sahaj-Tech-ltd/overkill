package web

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Tiny RFC6455 implementation. We only need server-side text frames + ping
// handling, so pulling in gorilla or nhooyr would be overkill.

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

type wsConn struct {
	conn   net.Conn
	rw     *bufio.ReadWriter
	writeM sync.Mutex
}

func upgradeWS(w http.ResponseWriter, r *http.Request) (*wsConn, error) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return nil, errors.New("not a websocket upgrade")
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		return nil, errors.New("missing Sec-WebSocket-Key")
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, errors.New("hijack unsupported")
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		return nil, err
	}
	sum := sha1.Sum([]byte(key + wsGUID))
	accept := base64.StdEncoding.EncodeToString(sum[:])
	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
	if _, err := conn.Write([]byte(resp)); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return &wsConn{conn: conn, rw: rw}, nil
}

func (c *wsConn) Close() error { return c.conn.Close() }

// WriteText sends one text frame. Caller must serialise calls — the mutex
// guards interleaving but does not buffer.
func (c *wsConn) WriteText(payload []byte) error {
	c.writeM.Lock()
	defer c.writeM.Unlock()
	return c.writeFrame(0x1, payload)
}

// WritePing sends a ping with no payload.
func (c *wsConn) WritePing() error {
	c.writeM.Lock()
	defer c.writeM.Unlock()
	return c.writeFrame(0x9, nil)
}

// WriteClose sends a close frame and closes the underlying connection.
func (c *wsConn) WriteClose() {
	c.writeM.Lock()
	_ = c.writeFrame(0x8, []byte{0x03, 0xe8})
	c.writeM.Unlock()
	_ = c.conn.Close()
}

func (c *wsConn) writeFrame(opcode byte, payload []byte) error {
	_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	hdr := []byte{0x80 | opcode}
	n := len(payload)
	switch {
	case n <= 125:
		hdr = append(hdr, byte(n))
	case n <= 0xFFFF:
		hdr = append(hdr, 126, 0, 0)
		binary.BigEndian.PutUint16(hdr[2:], uint16(n))
	default:
		hdr = append(hdr, 127, 0, 0, 0, 0, 0, 0, 0, 0)
		binary.BigEndian.PutUint64(hdr[2:], uint64(n))
	}
	if _, err := c.conn.Write(hdr); err != nil {
		return err
	}
	if n > 0 {
		if _, err := c.conn.Write(payload); err != nil {
			return err
		}
	}
	return nil
}

// ReadLoop drains incoming frames so the kernel buffer never blocks. We do not
// need client payloads (the browser only opens the stream), but we must
// respond to pings. Returns when the peer closes or the conn errors.
func (c *wsConn) ReadLoop() error {
	for {
		_ = c.conn.SetReadDeadline(time.Now().Add(75 * time.Second))
		b0, err := c.rw.ReadByte()
		if err != nil {
			return err
		}
		b1, err := c.rw.ReadByte()
		if err != nil {
			return err
		}
		opcode := b0 & 0x0F
		masked := (b1 & 0x80) != 0
		length := int(b1 & 0x7F)
		switch length {
		case 126:
			var ext [2]byte
			if _, err := io.ReadFull(c.rw, ext[:]); err != nil {
				return err
			}
			length = int(binary.BigEndian.Uint16(ext[:]))
		case 127:
			var ext [8]byte
			if _, err := io.ReadFull(c.rw, ext[:]); err != nil {
				return err
			}
			length = int(binary.BigEndian.Uint64(ext[:]))
		}
		// Reject pathological frame lengths BEFORE the allocation.
		// A 64-bit length passed straight into make([]byte, length)
		// is a single-frame OOM. 16 MiB is comfortably above any
		// legitimate JSON event we emit; longer frames are either a
		// bug or hostile.
		const maxWSFrameBytes = 16 * 1024 * 1024
		if length < 0 || length > maxWSFrameBytes {
			return fmt.Errorf("ws: frame length %d exceeds cap %d", length, maxWSFrameBytes)
		}
		var maskKey [4]byte
		if masked {
			if _, err := io.ReadFull(c.rw, maskKey[:]); err != nil {
				return err
			}
		}
		payload := make([]byte, length)
		if length > 0 {
			if _, err := io.ReadFull(c.rw, payload); err != nil {
				return err
			}
			if masked {
				for i := range payload {
					payload[i] ^= maskKey[i%4]
				}
			}
		}
		switch opcode {
		case 0x8: // close
			return nil
		case 0x9: // ping → pong
			c.writeM.Lock()
			_ = c.writeFrame(0xA, payload)
			c.writeM.Unlock()
		case 0xA: // pong — ignore
		default:
			// Text/binary/continuation frames from the client are ignored.
		}
	}
}

// Helper used in tests only.
func wsAcceptKey(key string) string {
	sum := sha1.Sum([]byte(key + wsGUID))
	return base64.StdEncoding.EncodeToString(sum[:])
}
