// Package automation — standing orders (master plan §7.1).
//
// A standing order is a one-line persistent instruction the agent injects
// into every system prompt: "always commit after green tests", "never
// touch the auth module without confirming", "prefer ripgrep over find".
//
// Persisted as JSON lines under ~/.overkill/standing-orders.jsonl so the user
// can hand-edit them. Loaded once at boot; mutate via Add/Remove.
package automation

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// StandingOrder is one always-on instruction. The optional
// Verify/Report fields encode the Execute-Verify-Report pattern
// (master plan §7.1 Layer 5): when set, the agent should not just
// honor `Text` but also run `Verify` afterwards and `Report` the
// outcome. Empty Verify/Report means a plain "always honor"
// directive.
type StandingOrder struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	Verify    string    `json:"verify,omitempty"`
	Report    string    `json:"report,omitempty"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

// OrdersFile is the JSONL store.
type OrdersFile struct {
	mu     sync.RWMutex
	path   string
	orders []StandingOrder
}

// NewOrdersFile opens (or creates) the file at path. Parses any existing
// rows; ignores malformed lines.
func NewOrdersFile(path string) (*OrdersFile, error) {
	if path == "" {
		return nil, errors.New("standing orders: empty path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	o := &OrdersFile{path: path}
	if err := o.load(); err != nil {
		return nil, err
	}
	return o, nil
}

func (o *OrdersFile) load() error {
	f, err := os.Open(o.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var so StandingOrder
		if err := json.Unmarshal([]byte(line), &so); err != nil {
			continue // skip malformed lines
		}
		o.orders = append(o.orders, so)
	}
	return sc.Err()
}

// All returns a copy of every order, including disabled ones.
func (o *OrdersFile) All() []StandingOrder {
	o.mu.RLock()
	defer o.mu.RUnlock()
	out := make([]StandingOrder, len(o.orders))
	copy(out, o.orders)
	return out
}

// Active returns enabled orders. Used by the agent's prompt builder.
func (o *OrdersFile) Active() []StandingOrder {
	o.mu.RLock()
	defer o.mu.RUnlock()
	out := make([]StandingOrder, 0, len(o.orders))
	for _, so := range o.orders {
		if so.Enabled {
			out = append(out, so)
		}
	}
	return out
}

// Add appends a new standing order and persists.
func (o *OrdersFile) Add(text string) (*StandingOrder, error) {
	return o.AddEVR(text, "", "")
}

// AddEVR is Add with the Execute-Verify-Report fields populated.
// Plain `Add` is the common case; `AddEVR` is for orders the agent
// itself promotes ("from now on always do X, verify with Y, report
// Z"). Either verify or report may be empty.
func (o *OrdersFile) AddEVR(text, verify, report string) (*StandingOrder, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("standing orders: text required")
	}
	so := StandingOrder{
		ID:        uuid.NewString(),
		Text:      text,
		Verify:    strings.TrimSpace(verify),
		Report:    strings.TrimSpace(report),
		Enabled:   true,
		CreatedAt: time.Now().UTC(),
	}
	o.mu.Lock()
	o.orders = append(o.orders, so)
	o.mu.Unlock()
	if err := o.flush(); err != nil {
		return nil, err
	}
	return &so, nil
}

// Remove deletes an order by ID.
func (o *OrdersFile) Remove(id string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]StandingOrder, 0, len(o.orders))
	found := false
	for _, so := range o.orders {
		if so.ID == id {
			found = true
			continue
		}
		out = append(out, so)
	}
	if !found {
		return fmt.Errorf("standing orders: %q not found", id)
	}
	o.orders = out
	return o.flushLocked()
}

// SetEnabled toggles enable state.
func (o *OrdersFile) SetEnabled(id string, enabled bool) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	for i := range o.orders {
		if o.orders[i].ID == id {
			o.orders[i].Enabled = enabled
			return o.flushLocked()
		}
	}
	return fmt.Errorf("standing orders: %q not found", id)
}

// PromptSnippet returns a system-prompt fragment containing all active orders.
// Empty when nothing is active.
func (o *OrdersFile) PromptSnippet() string {
	active := o.Active()
	if len(active) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("STANDING ORDERS (always honor):\n")
	for i, so := range active {
		fmt.Fprintf(&b, "%d. %s\n", i+1, so.Text)
		// Execute-Verify-Report continuation: render verify +
		// report on indented lines so the model reads them as
		// part of the same directive (§7.1 Layer 5).
		if so.Verify != "" {
			fmt.Fprintf(&b, "   verify: %s\n", so.Verify)
		}
		if so.Report != "" {
			fmt.Fprintf(&b, "   report: %s\n", so.Report)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (o *OrdersFile) flush() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.flushLocked()
}

func (o *OrdersFile) flushLocked() error {
	tmp := o.path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	for _, so := range o.orders {
		raw, _ := json.Marshal(so)
		w.Write(raw)
		w.WriteByte('\n')
	}
	if err := w.Flush(); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, o.path)
}
