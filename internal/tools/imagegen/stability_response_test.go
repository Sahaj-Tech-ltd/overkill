package imagegen

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/stretchr/testify/require"
)

func TestStabilityReturnsWholeImageAndRejectsOversize(t *testing.T) {
	for _, size := range []int64{(4 << 20) + 42, maxDownloadedImageBytes + 1} {
		t.Run(strconv.FormatInt(size, 10), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer fixture-only" {
					http.Error(w, "unauthorized", 401)
					return
				}
				w.Header().Set("Content-Type", "image/png")
				_, _ = io.Copy(w, io.LimitReader(imageZeros{}, size))
			}))
			defer server.Close()
			t.Setenv("STABILITY_URL", server.URL)
			tool := New(config.ImageGenConfig{Provider: "stability", StabilityKey: "fixture-only"})
			out, err := tool.Execute(t.Context(), json.RawMessage(`{"prompt":"controlled fixture"}`))
			if size > maxDownloadedImageBytes {
				require.ErrorContains(t, err, "50 MiB")
				require.Empty(t, out)
				return
			}
			require.NoError(t, err)
			var result ImageGenOutput
			require.NoError(t, json.Unmarshal(out, &result))
			require.Len(t, result.Images, 1)
			defer os.Remove(result.Images[0])
			info, err := os.Stat(result.Images[0])
			require.NoError(t, err)
			require.Equal(t, size, info.Size())
			require.Equal(t, os.FileMode(0600), info.Mode().Perm())
		})
	}
}
