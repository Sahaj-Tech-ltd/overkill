// Package multimodal — audio transcription via whisper.cpp.
//
// We shell out to `whisper-cli` (whisper.cpp's CLI binary) or
// `whisper` (OpenAI's Python reference). Either works; we probe in
// order and use whichever is on PATH. The CLI options differ but
// the contract we need from each is identical: in = audio file,
// out = transcript text.
//
// Why not bundle a Go transcription library: there isn't a good one.
// Whisper.cpp has Go bindings (mutablelogic/go-whisper, etc.) but
// they require model files at known paths + careful CGO setup —
// way out of scope for a "drop an audio file in the chat" feature.
// Shell-out keeps the dependency story honest: user installs
// whisper, we use it. No models bundled.
package multimodal

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// AudioExtractor transcribes audio via the first whisper binary on
// PATH (whisper-cli or whisper).
type AudioExtractor struct {
	// Model is passed to whisper.cpp via --model (path to a .bin
	// model) or to whisper via --model (model name like "base"). When
	// empty we let whisper pick its default. The user is expected to
	// have a model configured if they want better quality.
	Model string
	// Language hint passed through when non-empty.
	Language string
	// Timeout caps the per-extract budget. Default 5min — long
	// enough for typical voice notes, short enough that a
	// runaway transcribe doesn't hang the agent.
	Timeout time.Duration
}

// NewAudioExtractor returns an extractor with sensible defaults.
func NewAudioExtractor() *AudioExtractor {
	return &AudioExtractor{Timeout: 5 * time.Minute}
}

func (a *AudioExtractor) Name() string { return "whisper" }

func (a *AudioExtractor) Supports(mime, ext string) bool {
	if strings.HasPrefix(mime, "audio/") {
		return true
	}
	switch ext {
	case ".wav", ".mp3", ".m4a", ".flac", ".ogg", ".opus", ".webm":
		return true
	}
	return false
}

func (a *AudioExtractor) Extract(ctx context.Context, path string) (Result, error) {
	bin, mode := whisperBinary()
	if bin == "" {
		return Result{}, &ErrMissingDependency{
			Tool:      "whisper",
			InstallEx: "brew install whisper-cpp  /  pip install openai-whisper",
		}
	}

	to := a.Timeout
	if to <= 0 {
		to = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, to)
	defer cancel()

	// Both CLIs write the transcript to a side file by default. We
	// give them a tempdir + glob the output back.
	outDir, err := os.MkdirTemp("", "overkill-whisper-*")
	if err != nil {
		return Result{}, fmt.Errorf("whisper: tmpdir: %w", err)
	}
	defer os.RemoveAll(outDir)

	args := []string{}
	switch mode {
	case "whisper-cpp":
		args = append(args, "-f", path, "-otxt", "-of", filepath.Join(outDir, "out"))
		if a.Language != "" {
			args = append(args, "-l", a.Language)
		}
		if a.Model != "" {
			args = append(args, "-m", a.Model)
		}
	case "whisper-py":
		args = append(args, path, "--output_dir", outDir, "--output_format", "txt")
		if a.Language != "" {
			args = append(args, "--language", a.Language)
		}
		if a.Model != "" {
			args = append(args, "--model", a.Model)
		}
	}

	if _, err := exec.CommandContext(ctx, bin, args...).Output(); err != nil {
		return Result{}, fmt.Errorf("%s: %w", bin, err)
	}

	// Both modes drop a *.txt — grab the first.
	matches, _ := filepath.Glob(filepath.Join(outDir, "*.txt"))
	if len(matches) == 0 {
		return Result{}, fmt.Errorf("whisper: no transcript file in %s", outDir)
	}
	transcript, err := os.ReadFile(matches[0])
	if err != nil {
		return Result{}, fmt.Errorf("whisper: read transcript: %w", err)
	}

	meta := map[string]string{"backend": mode}
	if dur := audioDurationSeconds(ctx, path); dur > 0 {
		meta["duration_sec"] = strconv.Itoa(dur)
	}

	return Result{
		Text:      strings.TrimSpace(string(transcript)),
		Metadata:  meta,
		Extractor: a.Name(),
	}, nil
}

// whisperBinary probes for whisper-cli (whisper.cpp) and whisper (the
// Python reference) in order. Returns the binary path + a mode tag
// that drives the argv we build.
func whisperBinary() (string, string) {
	if p, err := exec.LookPath("whisper-cli"); err == nil {
		return p, "whisper-cpp"
	}
	if p, err := exec.LookPath("whisper"); err == nil {
		return p, "whisper-py"
	}
	return "", ""
}

// audioDurationSeconds asks ffprobe for the duration. Returns 0 on
// any failure — we tolerate a missing ffprobe (not every install
// has ffmpeg available). The transcript still lands; just no
// duration metadata.
func audioDurationSeconds(ctx context.Context, path string) int {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		return 0
	}
	out, err := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		path,
	).Output()
	if err != nil {
		return 0
	}
	sec, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		return 0
	}
	return int(sec)
}
