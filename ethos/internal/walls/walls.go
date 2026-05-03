// orphan: quality-gate runner (master plan §5.6); needs /walls slash command
package walls

type WallID int

const (
	WallOuroboros WallID = iota
	WallArchitecture WallID = iota
	WallTestQuality WallID = iota
)

func (w WallID) String() string {
	switch w {
	case WallOuroboros:
		return "ouroboros"
	case WallArchitecture:
		return "architecture"
	case WallTestQuality:
		return "test-quality"
	default:
		return "unknown"
	}
}

type Severity int

const (
	SeverityInfo Severity = iota
	SeverityWarning Severity = iota
	SeverityBlock Severity = iota
)

func (s Severity) String() string {
	switch s {
	case SeverityInfo:
		return "info"
	case SeverityWarning:
		return "warning"
	case SeverityBlock:
		return "block"
	default:
		return "unknown"
	}
}

type WallResult struct {
	Wall        WallID   `json:"wall"`
	Passed      bool     `json:"passed"`
	Severity    Severity `json:"severity"`
	Message     string   `json:"message"`
	Details     []string `json:"details,omitempty"`
	Suggestions []string `json:"suggestions,omitempty"`
}

type WallReport struct {
	Results []WallResult `json:"results"`
	Passed  bool         `json:"passed"`
	Blocked bool         `json:"blocked"`
}

func NewReport(results []WallResult) *WallReport {
	r := &WallReport{
		Results: results,
		Passed:  true,
		Blocked: false,
	}
	for _, res := range results {
		if !res.Passed {
			r.Passed = false
		}
		if res.Severity == SeverityBlock && !res.Passed {
			r.Blocked = true
		}
	}
	return r
}
