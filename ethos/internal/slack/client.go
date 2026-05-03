package slack

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
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

// SocketClient is a tiny client-side WebSocket for Slack's Socket Mode.
// Slack publishes envelopes; we ack each one. The client handles control
// frames (ping/close) and exposes a simple Events channel.
//
// We hand-roll the WS handshake to avoid pulling in nhooyr/gorilla. The
// existing wsConn under internal/web is server-side (it expects masked
// inbound frames and writes unmasked outbound frames); RFC 6455 requires
// the opposite for clients, so we keep the two implementations separate.
type SocketClient struct {
	APIBaseURL string       // override for tests; default https://slack.com/api
	AppToken   string       // xapp-...
	HTTP       *http.Client // for the connections.open call
	Dial       func(ctx context.Context, network, addr string) (net.Conn, error) // override for tests

	conn   net.Conn
	rw     *bufio.ReadWriter
	writeM sync.Mutex
}

// Connect performs the apps.connections.open handshake then upgrades to WS.
// Returns when the WS is fully open and the "hello" envelope has been read.
// On any error the underlying connection is closed before returning.
func (c *SocketClient) Connect(ctx context.Context) error {
	wsURL, err := openConnection(ctx, c.APIBaseURL, c.AppToken, c.HTTP)
	if err != nil {
		return err
	}
	return c.dialWS(ctx, wsURL)
}

func (c *SocketClient) dialWS(ctx context.Context, wsURL string) error {
	u, err := url.Parse(wsURL)
	if err != nil {
		return fmt.Errorf("slack: parse ws url: %w", err)
	}
	host := u.Host
	port := u.Port()
	if port == "" {
		port = "443"
	}
	if !strings.Contains(host, ":") {
		host = host + ":" + port
	}

	dial := c.Dial
	if dial == nil {
		dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
			d := &net.Dialer{Timeout: 10 * time.Second}
			raw, err := d.DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			if u.Scheme == "wss" {
				tlsConn := tls.Client(raw, &tls.Config{ServerName: u.Hostname()})
				if err := tlsConn.HandshakeContext(ctx); err != nil {
					_ = raw.Close()
					return nil, err
				}
				return tlsConn, nil
			}
			return raw, nil
		}
	}

	conn, err := dial(ctx, "tcp", host)
	if err != nil {
		return fmt.Errorf("slack: dial: %w", err)
	}

	// RFC 6455 client handshake.
	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		_ = conn.Close()
		return err
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)
	path := u.RequestURI()
	if path == "" {
		path = "/"
	}
	req := "GET " + path + " HTTP/1.1\r\n" +
		"Host: " + u.Host + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		_ = conn.Close()
		return err
	}
	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	resp, err := http.ReadResponse(rw.Reader, nil)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("slack: ws handshake: %w", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		_ = conn.Close()
		return fmt.Errorf("slack: ws handshake: status %d", resp.StatusCode)
	}
	expected := wsAcceptKey(key)
	if got := resp.Header.Get("Sec-WebSocket-Accept"); got != expected {
		_ = conn.Close()
		return errors.New("slack: ws handshake: bad accept key")
	}
	c.conn = conn
	c.rw = rw
	return nil
}

// Close terminates the WS, sending a close frame on a best-effort basis.
func (c *SocketClient) Close() error {
	if c.conn == nil {
		return nil
	}
	c.writeM.Lock()
	_ = c.writeFrame(0x8, []byte{0x03, 0xe8})
	c.writeM.Unlock()
	return c.conn.Close()
}

// SendAck writes the {"envelope_id":"..."} ack Slack expects after each
// events_api delivery.
func (c *SocketClient) SendAck(envelopeID string) error {
	body, err := json.Marshal(map[string]string{"envelope_id": envelopeID})
	if err != nil {
		return err
	}
	c.writeM.Lock()
	defer c.writeM.Unlock()
	return c.writeFrame(0x1, body)
}

// ReadEnvelope blocks until the next text frame arrives, decoding it as
// SocketEnvelope. Pings and pongs are handled transparently.
func (c *SocketClient) ReadEnvelope(deadline time.Time) (*SocketEnvelope, error) {
	for {
		if !deadline.IsZero() {
			_ = c.conn.SetReadDeadline(deadline)
		}
		opcode, payload, err := c.readFrame()
		if err != nil {
			return nil, err
		}
		switch opcode {
		case 0x1: // text
			var env SocketEnvelope
			if err := json.Unmarshal(payload, &env); err != nil {
				return nil, fmt.Errorf("slack: decode envelope: %w", err)
			}
			return &env, nil
		case 0x9: // ping → pong
			c.writeM.Lock()
			_ = c.writeFrame(0xA, payload)
			c.writeM.Unlock()
		case 0xA: // pong — ignore
		case 0x8: // close
			return nil, io.EOF
		default:
			// continuation / binary — ignore
		}
	}
}

// writeFrame sends a single client frame. Client frames MUST be masked
// (RFC 6455 §5.3). We use a small per-frame random mask key.
func (c *SocketClient) writeFrame(opcode byte, payload []byte) error {
	_ = c.conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
	hdr := []byte{0x80 | opcode}
	n := len(payload)
	switch {
	case n <= 125:
		hdr = append(hdr, 0x80|byte(n))
	case n <= 0xFFFF:
		hdr = append(hdr, 0x80|126, 0, 0)
		binary.BigEndian.PutUint16(hdr[2:], uint16(n))
	default:
		hdr = append(hdr, 0x80|127, 0, 0, 0, 0, 0, 0, 0, 0)
		binary.BigEndian.PutUint64(hdr[2:], uint64(n))
	}
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return err
	}
	hdr = append(hdr, mask[:]...)
	masked := make([]byte, n)
	for i := 0; i < n; i++ {
		masked[i] = payload[i] ^ mask[i%4]
	}
	if _, err := c.conn.Write(hdr); err != nil {
		return err
	}
	if n > 0 {
		if _, err := c.conn.Write(masked); err != nil {
			return err
		}
	}
	return nil
}

// readFrame reads a single (possibly masked) frame from the server. Slack
// servers do not mask frames they send to clients, but we tolerate either.
func (c *SocketClient) readFrame() (opcode byte, payload []byte, err error) {
	b0, err := c.rw.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	b1, err := c.rw.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	opcode = b0 & 0x0F
	masked := (b1 & 0x80) != 0
	length := int(b1 & 0x7F)
	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(c.rw, ext[:]); err != nil {
			return 0, nil, err
		}
		length = int(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(c.rw, ext[:]); err != nil {
			return 0, nil, err
		}
		length = int(binary.BigEndian.Uint64(ext[:]))
	}
	var maskKey [4]byte
	if masked {
		if _, err := io.ReadFull(c.rw, maskKey[:]); err != nil {
			return 0, nil, err
		}
	}
	payload = make([]byte, length)
	if length > 0 {
		if _, err := io.ReadFull(c.rw, payload); err != nil {
			return 0, nil, err
		}
		if masked {
			for i := range payload {
				payload[i] ^= maskKey[i%4]
			}
		}
	}
	return opcode, payload, nil
}

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

func wsAcceptKey(key string) string {
	sum := sha1.Sum([]byte(key + wsGUID))
	return base64.StdEncoding.EncodeToString(sum[:])
}
