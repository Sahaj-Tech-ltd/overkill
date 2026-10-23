package coordination

import "time"

type UsageReconciliationTotals struct {
	InputTokens       int64   `json:"input_tokens"`
	OutputTokens      int64   `json:"output_tokens"`
	CachedInputTokens int64   `json:"cached_input_tokens"`
	EstimatedCostUSD  float64 `json:"estimated_cost_usd"`
	UnknownUsage      int64   `json:"unknown_usage"`
	UnknownPrice      int64   `json:"unknown_price"`
}
type UsageJournalRun struct {
	OwnerID  string `json:"owner_id"`
	Finished bool   `json:"finished"`
	Resolved bool   `json:"resolved"`
}
type UsageSampleEvidence struct {
	Key               string  `json:"key"`
	InputTokens       int64   `json:"input_tokens"`
	OutputTokens      int64   `json:"output_tokens"`
	CachedInputTokens int64   `json:"cached_input_tokens"`
	EstimatedCostUSD  float64 `json:"estimated_cost_usd"`
	UsageKnown        bool    `json:"usage_known"`
	PriceKnown        bool    `json:"price_known"`
}
type UsageReconciliationRecord struct {
	ReconciliationID string                    `json:"reconciliation_id"`
	MessageID        string                    `json:"message_id"`
	ActorPeerID      string                    `json:"actor_peer_id"`
	Basis            string                    `json:"basis"`
	Reason           string                    `json:"reason"`
	Evidence         string                    `json:"evidence"`
	Provenance       string                    `json:"provenance"`
	Before           UsageReconciliationTotals `json:"before"`
	After            UsageReconciliationTotals `json:"after"`
	CreatedAt        time.Time                 `json:"created_at"`
}
type UsageReconciliationSnapshot struct {
	contributionCostNano int64
	knownCostNano        int64
	MessageID            string                      `json:"message_id"`
	MessageRevision      int64                       `json:"message_revision"`
	UsageRevision        int64                       `json:"usage_revision"`
	Scope                string                      `json:"scope"`
	OwnerPeerID          string                      `json:"owner_peer_id"`
	JournalFingerprint   string                      `json:"journal_fingerprint"`
	CanReconcile         bool                        `json:"can_reconcile"`
	Reason               string                      `json:"reason"`
	Runs                 []UsageJournalRun           `json:"runs"`
	RunCount             int64                       `json:"run_count"`
	RunsTruncated        bool                        `json:"runs_truncated"`
	Samples              []UsageSampleEvidence       `json:"samples"`
	SampleCount          int64                       `json:"sample_count"`
	SamplesTruncated     bool                        `json:"samples_truncated"`
	Observed             UsageReconciliationTotals   `json:"observed"`
	Contribution         UsageReconciliationTotals   `json:"contribution"`
	KnownCostFloorUSD    float64                     `json:"known_cost_floor_usd"`
	Previous             []UsageReconciliationRecord `json:"previous"`
	PreviousCount        int64                       `json:"previous_count"`
	PreviousTruncated    bool                        `json:"previous_truncated"`
}
type ReconcileTaskUsageInput struct {
	MessageID                  string  `json:"message_id"`
	ExpectedMessageRevision    int64   `json:"expected_message_revision"`
	ExpectedUsageRevision      int64   `json:"expected_usage_revision"`
	ExpectedJournalFingerprint string  `json:"expected_journal_fingerprint"`
	ReconciliationID           string  `json:"reconciliation_id"`
	InputTokens                int64   `json:"input_tokens"`
	OutputTokens               int64   `json:"output_tokens"`
	CachedInputTokens          int64   `json:"cached_input_tokens"`
	CostUSD                    float64 `json:"cost_usd"`
	UsageKnown                 bool    `json:"usage_known"`
	PriceKnown                 bool    `json:"price_known"`
	Reason                     string  `json:"reason"`
	Evidence                   string  `json:"evidence"`
	Basis                      string  `json:"basis"`
	AcknowledgeUncertainty     bool    `json:"acknowledge_uncertainty"`
}
type ReconcileTaskUsageResult struct {
	Usage          TaskUsage                   `json:"usage"`
	Reconciliation UsageReconciliationRecord   `json:"reconciliation"`
	Snapshot       UsageReconciliationSnapshot `json:"snapshot"`
}

type UsageReconciliationCandidate struct {
	UnknownUsage          int64  `json:"unknown_usage"`
	UnknownPrice          int64  `json:"unknown_price"`
	MessageID             string `json:"message_id"`
	MessageRevision       int64  `json:"message_revision"`
	State                 State  `json:"state"`
	PendingExecutions     int64  `json:"pending_executions"`
	UnconfirmedExecutions int64  `json:"unconfirmed_executions"`
	CanReconcile          bool   `json:"can_reconcile"`
	Reason                string `json:"reason"`
}
type UsageReconciliationCandidatePage struct {
	Scope      string                         `json:"scope"`
	Candidates []UsageReconciliationCandidate `json:"candidates"`
	NextCursor string                         `json:"next_cursor"`
	Truncated  bool                           `json:"truncated"`
}
