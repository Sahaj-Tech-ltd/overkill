package skills

import (
	"time"
)

type Skill struct {
	Name         string            `json:"name"`
	Version      string            `json:"version"`
	Description  string            `json:"description"`
	Author       string            `json:"author"`
	Category     string            `json:"category"`
	Tags         []string          `json:"tags"`
	Triggers     []string          `json:"triggers"`
	Instructions string            `json:"instructions"`
	FilePath     string            `json:"file_path"`
	Bundled      bool              `json:"bundled"`
	Enabled      bool              `json:"enabled"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
	Metadata     map[string]string `json:"metadata"`
}

type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}
