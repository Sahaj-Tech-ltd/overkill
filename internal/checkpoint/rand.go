package checkpoint

import (
	"crypto/rand"
	"io"
)

// newRandSource returns crypto/rand.Reader. Wrapped so tests can swap in a
// deterministic source via randSource = ...
func newRandSource() io.Reader { return rand.Reader }
