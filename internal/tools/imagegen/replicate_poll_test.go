package imagegen

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/stretchr/testify/require"
)

type pollContextBody struct {
	ctx    context.Context
	reader io.Reader
	reads  *int
}

func (b *pollContextBody) Read(p []byte) (int, error) {
	*b.reads++
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	return b.reader.Read(p)
}
func (b *pollContextBody) Close() error { return nil }

func TestReplicateConsumesPollBodyBeforeCancellingRequest(t *testing.T) {
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	reads, requests := 0, 0
	http.DefaultTransport = imageRoundTripper(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.Header.Get("Authorization") != "Bearer fixture-only" {
			t.Errorf("missing configured bearer")
		}
		var body io.ReadCloser
		if r.Method == http.MethodPost {
			body = io.NopCloser(strings.NewReader(`{"id":"fixture-id","status":"starting"}`))
		} else {
			require.Equal(t, "https://api.replicate.com/v1/predictions/fixture-id", r.URL.String())
			body = &pollContextBody{ctx: r.Context(), reader: strings.NewReader(`{"id":"fixture-id","status":"succeeded","output":"http://127.0.0.1/untrusted-image"}`), reads: &reads}
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: body}, nil
	})
	tool := New(config.ImageGenConfig{Provider: "replicate", ReplicateToken: "fixture-only"})
	_, err := tool.Execute(t.Context(), json.RawMessage(`{"prompt":"local controlled protocol"}`))
	require.ErrorContains(t, err, "download requires public HTTPS")
	require.Greater(t, reads, 0)
	require.Equal(t, 2, requests)
}
