// Package personality — local random helpers.
// Go 1.20+ auto-seeds the global rand source; no init needed.
package personality

import (
	"crypto/rand"
	"math/big"
)

// localRand returns a secure random integer in [0, n).
func localRand(n int) int {
	if n <= 0 {
		return 0
	}
	bigN := big.NewInt(int64(n))
	val, err := rand.Int(rand.Reader, bigN)
	if err != nil {
		return 0
	}
	return int(val.Int64())
}
