// notes plugin: capture quick notes from chat. Adds /note, /notes commands
// and note_add, note_search tools.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	sdk "github.com/Sahaj-Tech-ltd/ethos/examples/plugins/sdk-go"
)

type note struct {
	Timestamp string `json:"ts"`
	SessionID string `json:"session_id"`
	Text      string `json:"text"`
}

func notesPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "notes.jsonl"
	}
	return filepath.Join(home, ".ethos", "notes.jsonl")
}

func appendNote(sessionID, text string) error {
	path := notesPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	n := note{Timestamp: time.Now().UTC().Format(time.RFC3339), SessionID: sessionID, Text: text}
	b, _ := json.Marshal(n)
	_, err = f.Write(append(b, '\n'))
	return err
}

func readNotes() ([]note, error) {
	data, err := os.ReadFile(notesPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []note
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var n note
		if err := json.Unmarshal([]byte(line), &n); err == nil {
			out = append(out, n)
		}
	}
	return out, nil
}

func main() {
	p := sdk.New(sdk.Manifest{
		Name:        "notes",
		Version:     "0.1.0",
		Description: "Quick note capture from chat",
	})

	p.RegisterCommand(sdk.CommandDecl{ID: "note", Title: "/note", Description: "append a note"})
	p.RegisterCommand(sdk.CommandDecl{ID: "notes", Title: "/notes", Description: "show recent notes"})
	p.RegisterTool(sdk.ToolDecl{Name: "note_add", Description: "Append a note to ~/.ethos/notes.jsonl"})
	p.RegisterTool(sdk.ToolDecl{Name: "note_search", Description: "Search notes by substring match"})

	p.OnCommand("note", func(ctx context.Context, args string) error {
		text := strings.TrimSpace(args)
		if text == "" {
			return p.Toast(ctx, "warning", "usage: /note <text>")
		}
		sess, _ := p.Session(ctx)
		if err := appendNote(sess.ID, text); err != nil {
			return p.Toast(ctx, "error", "note: "+err.Error())
		}
		return p.Toast(ctx, "success", "note saved")
	})

	p.OnCommand("notes", func(ctx context.Context, args string) error {
		notes, err := readNotes()
		if err != nil {
			return p.Toast(ctx, "error", "notes: "+err.Error())
		}
		if len(notes) == 0 {
			return p.Toast(ctx, "info", "no notes yet")
		}
		recent := notes
		if len(recent) > 5 {
			recent = recent[len(recent)-5:]
		}
		var b strings.Builder
		for _, n := range recent {
			fmt.Fprintf(&b, "• %s\n", n.Text)
		}
		return p.Toast(ctx, "info", b.String())
	})

	p.OnTool("note_add", func(ctx context.Context, args json.RawMessage) (any, error) {
		var in struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, err
		}
		if in.Text == "" {
			return nil, fmt.Errorf("text required")
		}
		sess, _ := p.Session(ctx)
		if err := appendNote(sess.ID, in.Text); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true}, nil
	})

	p.OnTool("note_search", func(ctx context.Context, args json.RawMessage) (any, error) {
		var in struct {
			Query string `json:"query"`
		}
		_ = json.Unmarshal(args, &in)
		notes, err := readNotes()
		if err != nil {
			return nil, err
		}
		var hits []note
		q := strings.ToLower(in.Query)
		for _, n := range notes {
			if q == "" || strings.Contains(strings.ToLower(n.Text), q) {
				hits = append(hits, n)
			}
		}
		return map[string]any{"matches": hits, "count": len(hits)}, nil
	})

	if err := p.Run(); err != nil {
		// Ignore EOF — host closed stdin during shutdown.
		_ = err
	}
}
