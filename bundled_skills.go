// Package bundled ships the repo's skills/ tree inside the Overkill
// binary. BootstrapOverkillHome unpacks these into ~/.overkill/skills/
// on first run so every agent session starts with battle-tested skills.
package bundled

import "embed"

//go:embed skills
var SkillsFS embed.FS
