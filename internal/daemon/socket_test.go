package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// freshSocket returns a unique socket path inside t.TempDir.
func freshSocket(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "daemon.sock")
}

// echoServer is a tiny test fixture: handles "echo" + a slow "sleep" op.
// Most tests use it to exercise the protocol without touching real
// daemon machinery.
func echoServer(t *testing.T) (*Server, string) {
	t.Helper()
	path := freshSocket(t)
	s := NewServer(path)
	s.Register("echo", func(ctx context.Context, req Request) (Response, error) {
		return Response{Result: req.Params}, nil
	})
	s.Register("err", func(ctx context.Context, req Request) (Response, error) {
		return Response{}, errors.New("forced failure")
	})
	s.Register("slow", func(ctx context.Context, req Request) (Response, error) {
		select {
		case <-time.After(200 * time.Millisecond):
			return Response{Result: json.RawMessage(`"slept"`)}, nil
		case <-ctx.Done():
			return Response{}, ctx.Err()
		}
	})
	if err := s.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Stop)
	return s, path
}

func TestSocket_RoundtripEcho(t *testing.T) {
	_, path := echoServer(t)
	client := NewClient(path)

	raw, err := client.Call("echo", map[string]string{"hi": "there"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v (raw: %s)", err, raw)
	}
	if got["hi"] != "there" {
		t.Errorf("echo payload lost: %+v", got)
	}
}

func TestSocket_UnknownOpReturnsCode(t *testing.T) {
	_, path := echoServer(t)
	_, err := NewClient(path).Call("not_registered", nil)
	if err == nil {
		t.Fatal("expected error for unknown op")
	}
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("want RPCError, got %T: %v", err, err)
	}
	if rpcErr.Code != "unknown_op" {
		t.Errorf("code: got %q want unknown_op", rpcErr.Code)
	}
}

func TestSocket_HandlerErrorPropagates(t *testing.T) {
	_, path := echoServer(t)
	_, err := NewClient(path).Call("err", nil)
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("want RPCError, got %T: %v", err, err)
	}
	if rpcErr.Code != "internal" {
		t.Errorf("code: %q", rpcErr.Code)
	}
	if !strings.Contains(rpcErr.Message, "forced failure") {
		t.Errorf("message lost: %s", rpcErr.Message)
	}
}

func TestSocket_DaemonDownReturnsSentinel(t *testing.T) {
	// No server running on this path.
	path := freshSocket(t)
	_, err := NewClient(path).Call("ping", nil)
	if !errors.Is(err, ErrDaemonDown) {
		t.Errorf("expected ErrDaemonDown, got %v", err)
	}
}

func TestSocket_ClientTimeoutRespected(t *testing.T) {
	_, path := echoServer(t)
	// slow handler takes 200ms; we give the client 50ms.
	client := NewClient(path).WithTimeout(50 * time.Millisecond)
	_, err := client.Call("slow", nil)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	// Either i/o timeout from the deadline, or RPCError if the handler
	// happened to complete just before we hit the deadline. Both are
	// acceptable outcomes for a tight-timeout test.
}

func TestSocket_StopIsIdempotent(t *testing.T) {
	s, _ := echoServer(t)
	s.Stop()
	s.Stop() // second call must not panic
}

func TestSocket_StaleFileGetsCleanedOnStart(t *testing.T) {
	path := freshSocket(t)
	// Plant a stale file at the socket path.
	if err := writeFile(path, []byte("stale")); err != nil {
		t.Fatal(err)
	}
	s := NewServer(path)
	if err := s.Start(); err != nil {
		t.Fatalf("start should clean stale socket, got %v", err)
	}
	t.Cleanup(s.Stop)

	// Verify it really replaced the file with a socket by completing a
	// roundtrip.
	s.Register("ok", func(ctx context.Context, req Request) (Response, error) {
		return Response{Result: json.RawMessage(`true`)}, nil
	})
	if _, err := NewClient(path).Call("ok", nil); err != nil {
		t.Errorf("post-cleanup call failed: %v", err)
	}
}

func TestSocket_ConcurrentClients(t *testing.T) {
	_, path := echoServer(t)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			client := NewClient(path)
			payload := map[string]int{"i": i}
			raw, err := client.Call("echo", payload)
			if err != nil {
				t.Errorf("goroutine %d: %v", i, err)
				return
			}
			var got map[string]int
			_ = json.Unmarshal(raw, &got)
			if got["i"] != i {
				t.Errorf("goroutine %d: got payload %+v", i, got)
			}
		}(i)
	}
	wg.Wait()
}

func TestSocket_BadJSONReturnsBadRequest(t *testing.T) {
	_, path := echoServer(t)
	// Bypass the Client wrapper to send raw garbage.
	conn, err := dial(path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = conn.Write([]byte("{not json\n"))
	buf := make([]byte, 256)
	n, _ := conn.Read(buf)
	var resp Response
	_ = json.Unmarshal(buf[:n], &resp)
	if resp.Code != "bad_request" {
		t.Errorf("expected bad_request code, got %q (full: %s)", resp.Code, buf[:n])
	}
}

func TestSocketPath_UnderHome(t *testing.T) {
	p, err := SocketPath()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(p, ".overkill/daemon.sock") {
		t.Errorf("socket path not under .overkill: %s", p)
	}
}
