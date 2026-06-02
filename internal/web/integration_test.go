package web

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
)

// readWSFrame parses one server-to-client text frame (no masking, no
// fragmentation — fine for what the server actually sends).
func readWSFrame(r *bufio.Reader) ([]byte, byte, error) {
	b0, err := r.ReadByte()
	if err != nil {
		return nil, 0, err
	}
	b1, err := r.ReadByte()
	if err != nil {
		return nil, 0, err
	}
	opcode := b0 & 0x0F
	length := int(b1 & 0x7F)
	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			return nil, 0, err
		}
		length = int(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			return nil, 0, err
		}
		length = int(binary.BigEndian.Uint64(ext[:]))
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, 0, err
	}
	return payload, opcode, nil
}

func TestSendStreamsEvents(t *testing.T) {
	srv := NewServer(Config{NoAuth: true, Provider: "test", Version: "test", Agent: &fakeAgent{
		model: "gpt-test",
		events: []agent.StreamEvent{
			{Type: agent.EventToken, Content: "hello "},
			{Type: agent.EventToken, Content: "world"},
			{Type: agent.EventDone},
		},
	}})
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Shutdown(context.Background())

	addr := srv.Addr()

	// Open WS first so we don't miss events.
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	keyBytes := make([]byte, 16)
	_, _ = rand.Read(keyBytes)
	wsKey := base64.StdEncoding.EncodeToString(keyBytes)
	req := "GET /api/events HTTP/1.1\r\n" +
		"Host: " + addr + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + wsKey + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(conn)
	// Read response headers until empty line.
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("ws handshake: %v", err)
		}
		if strings.HasPrefix(line, "HTTP/") && !strings.Contains(line, " 101 ") {
			t.Fatalf("bad handshake: %s", line)
		}
		if line == "\r\n" {
			break
		}
	}

	// Now POST /api/send.
	body := strings.NewReader(`{"sessionId":"sX","text":"hi"}`)
	res, err := http.Post("http://"+addr+"/api/send", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()

	// Drain at least one text_delta and one done within a generous window.
	gotDelta, gotDone := false, false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !(gotDelta && gotDone) {
		_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		payload, op, err := readWSFrame(br)
		if err != nil {
			break
		}
		if op != 0x1 {
			continue
		}
		var ev wsEvent
		if err := json.Unmarshal(payload, &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "text_delta":
			gotDelta = true
		case "done":
			gotDone = true
		}
	}
	if !gotDelta || !gotDone {
		t.Errorf("expected delta+done; got delta=%v done=%v", gotDelta, gotDone)
	}
}
