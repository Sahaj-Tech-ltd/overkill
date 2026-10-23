package journal

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestJournalKernelLockSerializesDistinctHandles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock.jsonl")
	first, err := openJournalFile(path)
	require.NoError(t, err)
	defer first.Close()
	second, err := openJournalFile(path)
	require.NoError(t, err)
	defer second.Close()
	require.NoError(t, lockJournalFile(first))
	done := make(chan error, 1)
	go func() { done <- lockJournalFile(second) }()
	select {
	case err := <-done:
		t.Fatalf("second writer bypassed lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	require.NoError(t, unlockJournalFile(first))
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("writer did not acquire released lock")
	}
	require.NoError(t, unlockJournalFile(second))
}

func TestJournalConcurrentProcessesPreserveCompleteEntries(t *testing.T) {
	dir := t.TempDir()
	executable, err := os.Executable()
	require.NoError(t, err)
	const writers = 4
	children := make([]*exec.Cmd, writers)
	outputs := make([]bytes.Buffer, writers)
	for i := range children {
		cmd := exec.Command(executable, "-test.run=^TestJournalWriterHelperProcess$")
		cmd.Env = append(os.Environ(), "OVERKILL_JOURNAL_TEST_DIR="+dir, fmt.Sprintf("OVERKILL_JOURNAL_TEST_ID=%d", i))
		cmd.Stdout = &outputs[i]
		cmd.Stderr = &outputs[i]
		require.NoError(t, cmd.Start())
		children[i] = cmd
	}
	for i, child := range children {
		require.NoError(t, child.Wait(), outputs[i].String())
	}
	files, err := filepath.Glob(filepath.Join(dir, "raw", "*.jsonl"))
	require.NoError(t, err)
	require.NotEmpty(t, files)
	count := 0
	for _, path := range files {
		f, err := os.Open(path)
		require.NoError(t, err)
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 4096), 128*1024)
		for scanner.Scan() {
			var entry Entry
			require.NoError(t, json.Unmarshal(scanner.Bytes(), &entry))
			require.Len(t, entry.Content, 32*1024)
			count++
		}
		require.NoError(t, scanner.Err())
		f.Close()
	}
	require.Equal(t, writers*12, count)
}

func TestJournalWriterHelperProcess(t *testing.T) {
	dir := os.Getenv("OVERKILL_JOURNAL_TEST_DIR")
	if dir == "" {
		return
	}
	recorder := NewFlightRecorder(dir, os.Getenv("OVERKILL_JOURNAL_TEST_ID"))
	for i := 0; i < 12; i++ {
		require.NoError(t, recorder.RecordInput(strings.Repeat("x", 32*1024)))
	}
}

func TestReadDayNormalizesToUTC(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "raw"), 0750))
	path := filepath.Join(dir, "raw", "2026-10-21.jsonl")
	data, err := json.Marshal(Entry{ID: "utc-entry", Content: "after UTC midnight"})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, append(data, '\n'), 0600))
	pacific := time.FixedZone("PDT", -7*60*60)
	local := time.Date(2026, 10, 20, 22, 0, 0, 0, pacific)
	entries, err := NewFlightRecorder(dir, "").ReadDay(local)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "utc-entry", entries[0].ID)
}
