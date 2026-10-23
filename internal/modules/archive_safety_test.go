package modules

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type archiveEntry struct {
	name, body string
	kind       byte
}

func moduleArchive(t *testing.T, entries ...archiveEntry) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		kind := e.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		h := &tar.Header{Name: e.name, Typeflag: kind, Mode: 0o7777}
		if kind == tar.TypeReg {
			h.Size = int64(len(e.body))
		}
		require.NoError(t, tw.WriteHeader(h))
		if kind == tar.TypeReg {
			_, err := io.WriteString(tw, e.body)
			require.NoError(t, err)
		}
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return b.Bytes()
}

func TestModuleArchiveRejectsUnsafeOrOversizedPayloads(t *testing.T) {
	valid := moduleArchive(t, archiveEntry{name: "root/file", body: "payload"})
	cases := []struct {
		name   string
		data   []byte
		limits archiveLimits
	}{
		{"compressed", valid, archiveLimits{8, 4096, 1024, 10}},
		{"expanded", moduleArchive(t, archiveEntry{name: "root/one", body: strings.Repeat("x", 900)}, archiveEntry{name: "root/two", body: strings.Repeat("y", 900)}), archiveLimits{4096, 2000, 1024, 10}},
		{"file", valid, archiveLimits{4096, 4096, 3, 10}},
		{"entries", moduleArchive(t, archiveEntry{name: "root/a", body: "a"}, archiveEntry{name: "root/b", body: "b"}), archiveLimits{4096, 4096, 1024, 1}},
		{"parent", moduleArchive(t, archiveEntry{name: "root/../outside", body: "x"}), moduleArchiveLimits},
		{"absolute", moduleArchive(t, archiveEntry{name: "/root/file", body: "x"}), moduleArchiveLimits},
		{"windows", moduleArchive(t, archiveEntry{name: `root\outside`, body: "x"}), moduleArchiveLimits},
		{"multiple-roots", moduleArchive(t, archiveEntry{name: "root/file", body: "x"}, archiveEntry{name: "other/file", body: "y"}), moduleArchiveLimits},
		{"duplicate", moduleArchive(t, archiveEntry{name: "root/file", body: "x"}, archiveEntry{name: "root/file", body: "y"}), moduleArchiveLimits},
		{"case-collision", moduleArchive(t, archiveEntry{name: "root/File", body: "x"}, archiveEntry{name: "root/file", body: "y"}), moduleArchiveLimits},
		{"symlink", moduleArchive(t, archiveEntry{name: "root/link", kind: tar.TypeSymlink}), moduleArchiveLimits},
		{"hardlink", moduleArchive(t, archiveEntry{name: "root/link", kind: tar.TypeLink}), moduleArchiveLimits},
		{"empty", moduleArchive(t, archiveEntry{name: "root/", kind: tar.TypeDir}), moduleArchiveLimits},
		{"gzip-truncated", valid[:len(valid)-5], moduleArchiveLimits},
	}
	corrupt := append([]byte(nil), valid...)
	corrupt[len(corrupt)-8] ^= 1
	cases = append(cases, struct {
		name   string
		data   []byte
		limits archiveLimits
	}{"checksum", corrupt, moduleArchiveLimits})
	// Valid gzip, but a tar file advertises more data than it actually carries.
	reader, err := gzip.NewReader(bytes.NewReader(valid))
	require.NoError(t, err)
	raw, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	var truncated bytes.Buffer
	gz := gzip.NewWriter(&truncated)
	_, err = gz.Write(raw[:512+3])
	require.NoError(t, err)
	require.NoError(t, gz.Close())
	cases = append(cases, struct {
		name   string
		data   []byte
		limits archiveLimits
	}{"tar-truncated", truncated.Bytes(), moduleArchiveLimits})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Error(t, extractTarGzLimited(bytes.NewReader(tc.data), t.TempDir(), tc.limits))
		})
	}
}

func TestModuleArchiveRetainsNestedLayoutAndMasksModes(t *testing.T) {
	dir := t.TempDir()
	data := moduleArchive(t, archiveEntry{name: "repo/docs/guide.md", body: "guide"}, archiveEntry{name: "repo/SKILL.md", body: "instructions"})
	require.NoError(t, extractTarGz(bytes.NewReader(data), dir))
	text, err := os.ReadFile(filepath.Join(dir, "docs/guide.md"))
	require.NoError(t, err)
	require.Equal(t, "guide", string(text))
	info, err := os.Stat(filepath.Join(dir, "SKILL.md"))
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		require.Equal(t, os.FileMode(0), info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky|0o022))
	} // Windows uses inherited ACLs rather than POSIX write/set-id mode bits.
}

func TestModuleArchivePublicationPreservesInstalledVersionOnFailure(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "module")
	require.NoError(t, os.Mkdir(dest, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dest, "old"), []byte("keep"), 0o600))
	invalid := moduleArchive(t, archiveEntry{name: "root/../escape", body: "bad"})
	require.Error(t, installArchive(bytes.NewReader(invalid), dest, func() error { t.Fatal("persist called before validation"); return nil }))
	old, err := os.ReadFile(filepath.Join(dest, "old"))
	require.NoError(t, err)
	require.Equal(t, "keep", string(old))
	valid := moduleArchive(t, archiveEntry{name: "root/new", body: "new"})
	failure := errors.New("manifest unavailable")
	require.ErrorIs(t, installArchive(bytes.NewReader(valid), dest, func() error { return failure }), failure)
	old, err = os.ReadFile(filepath.Join(dest, "old"))
	require.NoError(t, err)
	require.Equal(t, "keep", string(old))
	_, err = os.Stat(filepath.Join(dest, "new"))
	require.ErrorIs(t, err, os.ErrNotExist)
	require.NoError(t, installArchive(bytes.NewReader(valid), dest, func() error {
		data, err := os.ReadFile(filepath.Join(dest, "new"))
		require.NoError(t, err)
		require.Equal(t, "new", string(data))
		return nil
	}))
	_, err = os.Stat(filepath.Join(dest, "old"))
	require.ErrorIs(t, err, os.ErrNotExist)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

type moduleRoundTrip func(*http.Request) (*http.Response, error)

func (f moduleRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func moduleHTTPFixture(t *testing.T, fn moduleRoundTrip) {
	t.Helper()
	old := httpClient
	httpClient = &http.Client{Transport: fn}
	t.Cleanup(func() { httpClient = old })
}
func moduleResponse(code int, body []byte) *http.Response {
	return &http.Response{StatusCode: code, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header)}
}

func TestModuleUpdateRestoresTreeAndVersionWhenManifestFails(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManager(dir)
	require.NoError(t, err)
	mod := m.Get("superpowers")
	mod.Version = "v1"
	require.NoError(t, m.save())
	dest := filepath.Join(dir, "modules", mod.Name)
	require.NoError(t, os.MkdirAll(dest, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dest, "old"), []byte("keep"), 0o600))
	archive := moduleArchive(t, archiveEntry{name: "root/new", body: "updated"})
	moduleHTTPFixture(t, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/releases/latest") {
			return moduleResponse(200, []byte(`{"tag_name":"v2"}`)), nil
		}
		// Turn the manifest target into a directory after discovery, so its
		// atomic rename fails after the staged tree has been installed.
		require.NoError(t, os.Remove(m.manifestPath))
		require.NoError(t, os.Mkdir(m.manifestPath, 0o700))
		return moduleResponse(200, archive), nil
	})
	result, err := m.Update(mod.Name)
	require.ErrorContains(t, err, "persist updated manifest")
	require.False(t, result.Updated)
	require.Equal(t, "v1", mod.Version)
	old, err := os.ReadFile(filepath.Join(dest, "old"))
	require.NoError(t, err)
	require.Equal(t, "keep", string(old))
	_, err = os.Stat(filepath.Join(dest, "new"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestModuleSparseLogicalExpansionCannotBypassAggregateLimit(t *testing.T) {
	data, err := os.ReadFile("testdata/sparse-logical-limit.tgz")
	require.NoError(t, err)
	require.Less(t, len(data), 4096)
	err = extractTarGzLimited(bytes.NewReader(data), t.TempDir(), archiveLimits{4096, 8000, 4096, 10})
	require.ErrorContains(t, err, "logical expansion")
	// Sparse files within the same logical/physical budgets remain supported.
	dir := t.TempDir()
	require.NoError(t, extractTarGzLimited(bytes.NewReader(data), dir, archiveLimits{4096, 16000, 4096, 10}))
	for _, name := range []string{"file0", "file1", "file2"} {
		body, err := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err)
		require.Len(t, body, 3000)
		require.Equal(t, byte('x'), body[0])
		require.Equal(t, make([]byte, 2999), body[1:])
	}
}
