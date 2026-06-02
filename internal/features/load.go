package features

import (
	"fmt"
	"os"

	"github.com/pelletier/go-toml/v2"
)

// flagsFile is the TOML layout for ~/.overkill/features.toml.
//
//	[bridge_compressor]
//	default = false
//	percent = 0
//	[bridge_compressor.users]
//	"alice" = true
//	[bridge_compressor.channels]
//	"slack" = false
//
// Each top-level key is a flag name; the table under it is a Flag.
type flagsFile = map[string]Flag

// LoadFromTOML reads a features.toml and registers every flag it
// contains. Missing file is OK. Malformed file returns the parse
// error so the caller can surface it to the user.
func (m *Manager) LoadFromTOML(path string) error {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("features: read %s: %w", path, err)
	}
	var raw flagsFile
	if err := toml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("features: parse %s: %w", path, err)
	}
	for name, f := range raw {
		ff := f
		ff.Name = name
		m.Register(&ff)
	}
	return nil
}
