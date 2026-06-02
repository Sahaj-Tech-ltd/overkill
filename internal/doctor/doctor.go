// orphan: 'overkill doctor' command runs subset; remaining checks here pending integration into cmd/overkill/doctor.go
package doctor

import "encoding/json"

type CheckID string

type Status int

const (
	StatusOK    Status = iota
	StatusWarn  Status = iota
	StatusFail  Status = iota
	StatusFixed Status = iota
)

func (s Status) String() string {
	switch s {
	case StatusOK:
		return "ok"
	case StatusWarn:
		return "warn"
	case StatusFail:
		return "fail"
	case StatusFixed:
		return "fixed"
	default:
		return "unknown"
	}
}

func (s Status) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

type CheckResult struct {
	ID         CheckID `json:"id"`
	Name       string  `json:"name"`
	Status     Status  `json:"status"`
	Message    string  `json:"message"`
	Fixed      bool    `json:"fixed"`
	FixApplied string  `json:"fix_applied,omitempty"`
}

type Report struct {
	Results []CheckResult `json:"results"`
	Total   int           `json:"total"`
	Passed  int           `json:"passed"`
	Failed  int           `json:"failed"`
	Fixed   int           `json:"fixed"`
}
