package security

type ThreatLevel int

const (
	ThreatNone ThreatLevel = iota
	ThreatLow
	ThreatMedium
	ThreatHigh
	ThreatCritical
)

func (l ThreatLevel) String() string {
	switch l {
	case ThreatNone:
		return "none"
	case ThreatLow:
		return "low"
	case ThreatMedium:
		return "medium"
	case ThreatHigh:
		return "high"
	case ThreatCritical:
		return "critical"
	default:
		return "unknown"
	}
}

type Finding struct {
	Type        string
	Level       ThreatLevel
	Description string
	Match       string
	Confidence  float64
}

type ScanResult struct {
	Findings  []Finding
	MaxLevel  ThreatLevel
	Blocked   bool
	Sanitized string
}

type Scanner interface {
	Scan(input string) (*ScanResult, error)
	Name() string
}
