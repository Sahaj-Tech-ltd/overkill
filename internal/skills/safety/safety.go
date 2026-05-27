// Package safety — antivirus scanning for skill files (§6.4).
//
// Why: skills live as YAML + scripts on disk. Today the loader
// trusts whatever it finds under the user's skills dir. If the
// agent or the user installs an untrusted skill, malicious scripts
// run inside the agent's shell with the user's privileges. We need
// a gate.
//
// Design:
//
//   - Scanner is a small interface: Scan(path) → Result. Real impl
//     is VirusTotal (free tier, hash-lookup API — no upload, no
//     opening files to the network). A future ClamAV / local-yara
//     impl drops in behind the same interface.
//   - The default no-op scanner returns clean for everything.
//     Callers that don't wire a real scanner get today's behaviour
//     unchanged. This keeps the path open for users without a VT
//     key while making the gate available when they opt in.
//   - Result carries a verdict (Clean / Suspicious / Malicious /
//     Unknown) and a one-line reason. Loader policy decides what to
//     do with each verdict (block on Malicious, surface on
//     Suspicious, log on Unknown).
//   - File hashing is SHA-256. VT supports MD5/SHA1/SHA256 lookups;
//     we pick the strongest to avoid collision attacks against the
//     lookup index.
package safety

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Verdict is the coarse classification a Scanner returns. Loader
// callers branch on this:
//
//   - VerdictClean      : file's hash is known and benign, or
//     scanner is the no-op default.
//   - VerdictUnknown    : scanner has no record of this file —
//     either too new or never seen. Loader
//     may choose to allow with a warning.
//   - VerdictSuspicious : at least one engine flagged the file but
//     not enough to count as malicious. Loader
//     should surface to the user but may allow.
//   - VerdictMalicious  : confirmed bad. Loader MUST block.
type Verdict string

const (
	VerdictClean      Verdict = "clean"
	VerdictUnknown    Verdict = "unknown"
	VerdictSuspicious Verdict = "suspicious"
	VerdictMalicious  Verdict = "malicious"
)

// Result is one scanner finding for one file. The reason is
// suitable for surfacing in error messages and journal entries.
type Result struct {
	Path    string
	SHA256  string
	Verdict Verdict
	Reason  string
	// Detections is the engine_name → label map when available. Empty
	// for clean / unknown results.
	Detections map[string]string
}

// Scanner is the minimal surface the loader needs. Implementations
// must be context-aware and time-bounded — a stuck scan on one file
// should never block the entire skill load. Implementations should
// return (Unknown, nil) rather than an error for "I don't know"
// states so the caller can apply policy uniformly.
type Scanner interface {
	Scan(ctx context.Context, path string) (Result, error)
	Name() string
}

// NoopScanner returns Clean for every input. Used when no API key
// is configured — preserves today's open-by-default behaviour.
type NoopScanner struct{}

func (NoopScanner) Name() string { return "noop" }
func (NoopScanner) Scan(_ context.Context, path string) (Result, error) {
	return Result{Path: path, Verdict: VerdictClean, Reason: "no scanner configured"}, nil
}

// VirusTotalScanner queries the VT v3 public API by file hash.
// Free tier: 4 requests/min, 500/day, 15.5k/month — fine for skill
// installs which happen rarely. Lookup-only: we never UPLOAD files
// (privacy + bandwidth), only compute a local hash and ask "have
// you seen this?".
//
// Construct with NewVirusTotalScanner(apiKey). Empty key → returns
// nil and the caller should fall back to NoopScanner.
type VirusTotalScanner struct {
	apiKey  string
	baseURL string
	client  *http.Client
	// Thresholds — a file is malicious if more than maliciousMin
	// engines flag it; suspicious if more than suspiciousMin do but
	// below the malicious bar.
	maliciousMin  int
	suspiciousMin int
}

// NewVirusTotalScanner returns a configured scanner, or nil if the
// API key is empty. nil is a sentinel for "no scanner, use noop".
func NewVirusTotalScanner(apiKey string) *VirusTotalScanner {
	if strings.TrimSpace(apiKey) == "" {
		return nil
	}
	return &VirusTotalScanner{
		apiKey:        apiKey,
		baseURL:       "https://www.virustotal.com/api/v3",
		client:        &http.Client{Timeout: 15 * time.Second},
		maliciousMin:  2, // 2+ engines = malicious
		suspiciousMin: 1, // 1 engine = suspicious
	}
}

// WithBaseURL is for tests — point the scanner at an httptest
// server. Production callers leave it alone.
func (s *VirusTotalScanner) WithBaseURL(u string) *VirusTotalScanner {
	s.baseURL = strings.TrimRight(u, "/")
	return s
}

func (s *VirusTotalScanner) Name() string { return "virustotal" }

// Scan hashes the file and queries VT. Returns Unknown when VT has
// no record (404) — the caller decides whether to allow.
func (s *VirusTotalScanner) Scan(ctx context.Context, path string) (Result, error) {
	sum, err := sha256File(path)
	if err != nil {
		return Result{Path: path}, fmt.Errorf("safety: hash %s: %w", path, err)
	}
	res := Result{Path: path, SHA256: sum, Verdict: VerdictUnknown}

	url := s.baseURL + "/files/" + sum
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return res, fmt.Errorf("safety: build request: %w", err)
	}
	req.Header.Set("x-apikey", s.apiKey)

	resp, err := s.client.Do(req)
	if err != nil {
		return res, fmt.Errorf("safety: vt request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// VT hasn't seen this hash. Treat as Unknown — caller policy
		// decides whether to allow or quarantine.
		res.Reason = "not in VirusTotal corpus"
		return res, nil
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		// Rate-limited. Treat as Unknown so a quota miss doesn't
		// block legitimate skills.
		res.Reason = "VirusTotal rate-limited"
		return res, nil
	}
	if resp.StatusCode != http.StatusOK {
		return res, fmt.Errorf("safety: vt status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return res, fmt.Errorf("safety: read body: %w", err)
	}
	parsed, err := parseVTResponse(body)
	if err != nil {
		return res, fmt.Errorf("safety: parse vt: %w", err)
	}
	res.Detections = parsed.Detections
	res.Verdict, res.Reason = s.classify(parsed)
	return res, nil
}

// vtStats holds the fields we care about from the VT v3 response.
type vtStats struct {
	Malicious  int
	Suspicious int
	Detections map[string]string
}

func parseVTResponse(body []byte) (vtStats, error) {
	// VT v3 file lookup shape:
	// { "data": { "attributes": {
	//     "last_analysis_stats": {"malicious":N,"suspicious":N,...},
	//     "last_analysis_results": { "<engine>": {"category":"malicious","result":"..."} }
	// }}}
	var resp struct {
		Data struct {
			Attributes struct {
				LastAnalysisStats struct {
					Malicious  int `json:"malicious"`
					Suspicious int `json:"suspicious"`
				} `json:"last_analysis_stats"`
				LastAnalysisResults map[string]struct {
					Category string `json:"category"`
					Result   string `json:"result"`
				} `json:"last_analysis_results"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return vtStats{}, err
	}
	out := vtStats{
		Malicious:  resp.Data.Attributes.LastAnalysisStats.Malicious,
		Suspicious: resp.Data.Attributes.LastAnalysisStats.Suspicious,
		Detections: map[string]string{},
	}
	for engine, r := range resp.Data.Attributes.LastAnalysisResults {
		if r.Category == "malicious" || r.Category == "suspicious" {
			label := r.Result
			if label == "" {
				label = r.Category
			}
			out.Detections[engine] = label
		}
	}
	return out, nil
}

func (s *VirusTotalScanner) classify(st vtStats) (Verdict, string) {
	if st.Malicious >= s.maliciousMin {
		return VerdictMalicious, fmt.Sprintf("%d engine(s) flagged malicious", st.Malicious)
	}
	if st.Malicious+st.Suspicious >= s.suspiciousMin {
		return VerdictSuspicious, fmt.Sprintf("%d malicious, %d suspicious", st.Malicious, st.Suspicious)
	}
	return VerdictClean, "no engine detections"
}

// sha256File streams the file through SHA-256. Streaming avoids
// loading large blobs into memory; skills are typically small but
// the wrapped install path may scan bundled binaries.
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ScanDir walks dir and scans every regular file. Returns one
// Result per file plus the worst Verdict seen (Malicious >
// Suspicious > Unknown > Clean). Errors on individual files are
// folded into Unknown results so one unreadable file doesn't kill
// the whole scan.
func ScanDir(ctx context.Context, sc Scanner, dir string) ([]Result, Verdict, error) {
	if sc == nil {
		sc = NoopScanner{}
	}
	var results []Result
	worst := VerdictClean
	err := filepath.Walk(dir, func(p string, info os.FileInfo, werr error) error {
		if werr != nil {
			return werr
		}
		if info.IsDir() {
			return nil
		}
		r, serr := sc.Scan(ctx, p)
		if serr != nil {
			r = Result{Path: p, Verdict: VerdictUnknown, Reason: serr.Error()}
		}
		results = append(results, r)
		if verdictRank(r.Verdict) > verdictRank(worst) {
			worst = r.Verdict
		}
		return nil
	})
	return results, worst, err
}

func verdictRank(v Verdict) int {
	switch v {
	case VerdictMalicious:
		return 3
	case VerdictSuspicious:
		return 2
	case VerdictUnknown:
		return 1
	default:
		return 0
	}
}
