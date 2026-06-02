// Package security's pairing.go implements DM pairing — sender identity
// verification for messaging gateways. Stolen from OpenClaw's src/pairing/.
//
// OpenClaw's fix is dead simple: unknown senders get an 8-char code (no
// ambiguous 0/O/1/I), owner approves via CLI or web dashboard, sender gets
// added to per-channel allowFrom whitelist. Storage is JSON files with
// atomic temp+rename writes under ~/.overkill/pairing/.
package security

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// pairingAlphabet excludes ambiguous characters: 0, O, 1, I.
const pairingAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// pairingCodeLen is the length of generated codes.
const pairingCodeLen = 8

// pairingDefaultTTL is how long a pending code lives before expiry.
const pairingDefaultTTL = 60 * time.Minute

// pairingMaxPending is the maximum pending requests per account.
const pairingMaxPending = 3

// PairingMode controls sender verification behavior per channel.
type PairingMode string

const (
	// PairingOff — all senders are processed (legacy behavior).
	PairingOff PairingMode = "off"
	// PairingOn — unknown senders get a pairing challenge.
	PairingOn PairingMode = "pairing"
	// PairingOpen — all senders are processed (explicit opt-in to legacy).
	PairingOpen PairingMode = "open"
)

// PairingConfig is embedded in the main SecurityConfig.
type PairingConfig struct {
	Enabled    bool                   `json:"enabled"`
	Channels   map[string]PairingMode `json:"channels"`   // per-channel mode
	TTLMinutes int                    `json:"ttlMinutes"` // override default TTL
	MaxPending int                    `json:"maxPending"` // override max pending
}

// PairingRequest is a pending approval record for one sender.
type PairingRequest struct {
	ID         string    `json:"id"`        // sender ID (phone number, user ID)
	Code       string    `json:"code"`      // 8-char one-time code
	Channel    string    `json:"channel"`   // "telegram", "discord", etc.
	AccountID  string    `json:"accountId"` // multi-account support
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
}

// KnownSender is an approved sender on a channel.
type KnownSender struct {
	ID      string    `json:"id"`
	Channel string    `json:"channel"`
	AddedAt time.Time `json:"addedAt"`
	AddedBy string    `json:"addedBy"` // "cli" | "web"
}

// PairingStore manages pending pairing requests and approved senders.
// Storage: JSON files under ~/.overkill/pairing/ with atomic temp+rename.
type PairingStore struct {
	mu  sync.Mutex
	dir string // ~/.overkill/pairing/
}

// NewPairingStore wires the store to a directory. Creates the directory
// lazily on first write.
func NewPairingStore(dir string) *PairingStore {
	return &PairingStore{dir: dir}
}

// pairingFileName returns the pending requests file path for a channel.
func (ps *PairingStore) pairingFileName(channel string) string {
	return filepath.Join(ps.dir, safeChannelKey(channel)+"-pairing.json")
}

// allowFileName returns the approved senders file path for a channel+account.
func (ps *PairingStore) allowFileName(channel, accountID string) string {
	key := safeChannelKey(channel)
	if accountID != "" && accountID != "default" {
		key += "-" + safeChannelKey(accountID)
	}
	return filepath.Join(ps.dir, key+"-allowFrom.json")
}

// safeChannelKey sanitizes a channel ID against path traversal.
func safeChannelKey(s string) string {
	r := strings.NewReplacer(
		"/", "_", "\\", "_", ":", "_",
		"*", "_", "?", "_", "\"", "_",
		"<", "_", ">", "_", "|", "_",
		"..", "_",
	)
	return r.Replace(s)
}

// IssueChallenge creates a new pairing request for an unknown sender. If the
// sender already has a pending request, bumps LastSeenAt and reuses the
// code. If max pending is exceeded, the oldest (by LastSeenAt) is pruned.
func (ps *PairingStore) IssueChallenge(channel, senderID, accountID string) (*PairingRequest, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	requests, err := ps.readPendingLocked(channel)
	if err != nil {
		return nil, err
	}

	ttl := pairingDefaultTTL
	now := time.Now().UTC()

	// Purge expired.
	filtered := requests[:0]
	for _, r := range requests {
		if now.Sub(r.CreatedAt) < ttl {
			filtered = append(filtered, r)
		}
	}
	requests = filtered

	// Check if sender already has a pending request.
	for i, r := range requests {
		if r.ID == senderID && r.AccountID == accountID {
			requests[i].LastSeenAt = now
			if err := ps.writePendingLocked(channel, requests); err != nil {
				return nil, err
			}
			req := requests[i]
			return &req, nil
		}
	}

	// Generate a new code.
	code, err := randomCode(pairingCodeLen)
	if err != nil {
		return nil, fmt.Errorf("pairing: generate code: %w", err)
	}

	req := PairingRequest{
		ID:         senderID,
		Code:       code,
		Channel:    channel,
		AccountID:  accountID,
		CreatedAt:  now,
		LastSeenAt: now,
	}

	requests = append(requests, req)

	// Enforce max pending — prune oldest by LastSeenAt.
	maxPending := pairingMaxPending
	if len(requests) > maxPending {
		sort.Slice(requests, func(i, j int) bool {
			return requests[i].LastSeenAt.Before(requests[j].LastSeenAt)
		})
		requests = requests[len(requests)-maxPending:]
	}

	if err := ps.writePendingLocked(channel, requests); err != nil {
		return nil, err
	}
	return &req, nil
}

// ApproveCode looks up a pending request by code (case-insensitive match),
// removes it from pending, and adds the sender to the allowFrom whitelist.
func (ps *PairingStore) ApproveCode(channel, code string) (*KnownSender, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return nil, errors.New("pairing: code required")
	}

	requests, err := ps.readPendingLocked(channel)
	if err != nil {
		return nil, err
	}

	// Find the matching request.
	var found *PairingRequest
	remaining := requests[:0]
	for i, r := range requests {
		if strings.EqualFold(r.Code, code) {
			found = &requests[i]
		} else {
			remaining = append(remaining, r)
		}
	}

	if found == nil {
		return nil, fmt.Errorf("pairing: code %q not found for channel %s", code, channel)
	}

	// Reject expired codes. Codes may have been persisted to disk
	// before a daemon restart, so CreatedAt can be older than TTL.
	if time.Since(found.CreatedAt) > pairingDefaultTTL {
		// Remove expired from pending so it doesn't show up again.
		_ = ps.writePendingLocked(channel, remaining)
		return nil, fmt.Errorf("pairing: code %q expired for channel %s", code, channel)
	}

	// Remove from pending.
	if err := ps.writePendingLocked(channel, remaining); err != nil {
		return nil, err
	}

	// Add to allowFrom.
	allowList, err := ps.readAllowLocked(channel, found.AccountID)
	if err != nil {
		return nil, err
	}

	// Check for duplicate.
	for _, s := range allowList {
		if s.ID == found.ID && s.Channel == channel {
			return &KnownSender{
				ID:      found.ID,
				Channel: channel,
				AddedAt: found.CreatedAt,
				AddedBy: "cli",
			}, nil
		}
	}

	ks := KnownSender{
		ID:      found.ID,
		Channel: channel,
		AddedAt: time.Now().UTC(),
		AddedBy: "cli",
	}
	allowList = append(allowList, ks)

	if err := ps.writeAllowLocked(channel, found.AccountID, allowList); err != nil {
		return nil, err
	}
	return &ks, nil
}

// IsKnown reports whether a sender has been approved for a channel.
func (ps *PairingStore) IsKnown(channel, senderID, accountID string) bool {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	allowList, err := ps.readAllowLocked(channel, accountID)
	if err != nil {
		return false
	}
	for _, s := range allowList {
		if s.ID == senderID {
			return true
		}
	}
	return false
}

// RemoveSender removes a sender from the allowFrom whitelist.
func (ps *PairingStore) RemoveSender(channel, senderID, accountID string) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	allowList, err := ps.readAllowLocked(channel, accountID)
	if err != nil {
		return err
	}

	filtered := allowList[:0]
	found := false
	for _, s := range allowList {
		if s.ID == senderID {
			found = true
		} else {
			filtered = append(filtered, s)
		}
	}
	if !found {
		return fmt.Errorf("pairing: sender %q not found for channel %s", senderID, channel)
	}
	return ps.writeAllowLocked(channel, accountID, filtered)
}

// ListPending returns all pending pairing requests across all channels.
func (ps *PairingStore) ListPending() ([]PairingRequest, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	entries, err := os.ReadDir(ps.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var all []PairingRequest
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "-pairing.json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(ps.dir, e.Name()))
		if err != nil {
			continue
		}
		var store struct {
			Version  int              `json:"version"`
			Requests []PairingRequest `json:"requests"`
		}
		if err := json.Unmarshal(data, &store); err != nil {
			continue
		}
		all = append(all, store.Requests...)
	}

	sort.Slice(all, func(i, j int) bool {
		return all[i].CreatedAt.Before(all[j].CreatedAt)
	})
	return all, nil
}

// ListKnown returns all approved senders for a channel.
func (ps *PairingStore) ListKnown(channel string) ([]KnownSender, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	// We need to scan for all account-specific allow files for this channel.
	entries, err := os.ReadDir(ps.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	prefix := safeChannelKey(channel) + "-"
	suffix := "-allowFrom.json"

	var all []KnownSender
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(ps.dir, name))
		if err != nil {
			continue
		}
		var store struct {
			Version   int           `json:"version"`
			AllowFrom []KnownSender `json:"allowFrom"`
		}
		if err := json.Unmarshal(data, &store); err != nil {
			continue
		}
		all = append(all, store.AllowFrom...)
	}

	sort.Slice(all, func(i, j int) bool {
		return all[i].AddedAt.Before(all[j].AddedAt)
	})
	return all, nil
}

// ── internal helpers ────────────────────────────────────────────────────

func (ps *PairingStore) readPendingLocked(channel string) ([]PairingRequest, error) {
	path := ps.pairingFileName(channel)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var store struct {
		Version  int              `json:"version"`
		Requests []PairingRequest `json:"requests"`
	}
	if err := json.Unmarshal(data, &store); err != nil {
		return nil, fmt.Errorf("pairing: parse %s: %w", path, err)
	}
	return store.Requests, nil
}

func (ps *PairingStore) writePendingLocked(channel string, requests []PairingRequest) error {
	store := struct {
		Version  int              `json:"version"`
		Requests []PairingRequest `json:"requests"`
	}{Version: 1, Requests: requests}

	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(ps.pairingFileName(channel), data)
}

func (ps *PairingStore) readAllowLocked(channel, accountID string) ([]KnownSender, error) {
	path := ps.allowFileName(channel, accountID)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var store struct {
		Version   int           `json:"version"`
		AllowFrom []KnownSender `json:"allowFrom"`
	}
	if err := json.Unmarshal(data, &store); err != nil {
		return nil, fmt.Errorf("pairing: parse %s: %w", path, err)
	}
	// Filter out wildcards — they're never valid.
	filtered := store.AllowFrom[:0]
	for _, s := range store.AllowFrom {
		if s.ID != "" && s.ID != "*" {
			filtered = append(filtered, s)
		}
	}
	return filtered, nil
}

func (ps *PairingStore) writeAllowLocked(channel, accountID string, allowList []KnownSender) error {
	store := struct {
		Version   int           `json:"version"`
		AllowFrom []KnownSender `json:"allowFrom"`
	}{Version: 1, AllowFrom: allowList}

	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(ps.allowFileName(channel, accountID), data)
}

// atomicWrite writes data to a temp file and renames it over the target.
// This ensures a crash mid-write leaves the prior state intact.
func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("pairing: mkdir: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("pairing: write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("pairing: rename: %w", err)
	}
	return nil
}

// randomCode generates a cryptographically random code of the given length
// using the pairing alphabet.
func randomCode(length int) (string, error) {
	out := make([]byte, length)
	alphaLen := big.NewInt(int64(len(pairingAlphabet)))
	for i := range out {
		n, err := rand.Int(rand.Reader, alphaLen)
		if err != nil {
			return "", err
		}
		out[i] = pairingAlphabet[n.Int64()]
	}
	return string(out), nil
}
