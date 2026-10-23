package audit

import (
	"context"
	"fmt"
)

// CaptureBaseline stores a pre-task snapshot on this session's auditor. The
// client cannot substitute hashes or a different workspace in an audit RPC.
func (a *Auditor) CaptureBaseline(ctx context.Context) (*Snapshot, error) {
	snapshot, err := TakeSnapshot(ctx, a.WorkDir)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.baseline = snapshot
	a.mu.Unlock()
	return snapshot, nil
}
func (a *Auditor) AuditCaptured(ctx context.Context, claims []Claim) (*Report, error) {
	if len(claims) == 0 || len(claims) > 64 {
		return nil, fmt.Errorf("audit: 1..64 completion claims required")
	}
	for _, claim := range claims {
		if len(claim.Description) == 0 || len(claim.Description) > 4096 || len(claim.Files) > 128 {
			return nil, fmt.Errorf("audit: bounded description and file list required")
		}
	}
	a.mu.Lock()
	baseline := a.baseline
	a.mu.Unlock()
	if baseline == nil {
		return nil, fmt.Errorf("audit: capture a pre-task baseline first")
	}
	return a.Audit(ctx, baseline, claims), nil
}

type transcriptContextKey struct{}

// WithTranscript supplies actual session history as bounded evidence. It does
// not replace explicit completion claims or the stored filesystem baseline.
func WithTranscript(ctx context.Context, transcript string) context.Context {
	if len(transcript) > 16*1024 {
		transcript = transcript[len(transcript)-16*1024:]
	}
	return context.WithValue(ctx, transcriptContextKey{}, transcript)
}
