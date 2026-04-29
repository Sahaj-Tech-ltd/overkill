package diagnostic

import "time"

type DiagnosticReport struct {
	Error          string     `json:"error"`
	ErrorType      string     `json:"error_type"`
	AffectedFiles  []FileInfo `json:"affected_files"`
	RootCauses     []RootCause `json:"root_causes"`
	Approaches     []Approach `json:"approaches"`
	Confidence     float64    `json:"confidence"`
	Recommendation string     `json:"recommendation"`
	Timestamp      time.Time  `json:"timestamp"`
}

type FileInfo struct {
	Path         string `json:"path"`
	Role         string `json:"role"`
	Modified     bool   `json:"modified"`
	LinesAdded   int    `json:"lines_added"`
	LinesRemoved int    `json:"lines_removed"`
}

type RootCause struct {
	Description string   `json:"description"`
	File        string   `json:"file"`
	Line        int      `json:"line"`
	Confidence  float64  `json:"confidence"`
	Evidence    []string `json:"evidence"`
}

type Approach struct {
	ID            int      `json:"id"`
	Description   string   `json:"description"`
	Steps         []string `json:"steps"`
	Confidence    float64  `json:"confidence"`
	Risk          string   `json:"risk"`
	EstimatedTime string   `json:"estimated_time"`
	FilesToChange []string `json:"files_to_change"`
}
