package session

import "fmt"

// ErrMergeDiverged is returned by Merge when the parent has accumulated
// messages past the child's branch point.
var ErrMergeDiverged = fmt.Errorf("session: merge: parent diverged past branch point")

// copyMetadata returns a shallow copy of m, never nil.
func copyMetadata(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
