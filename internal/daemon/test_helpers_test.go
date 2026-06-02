package daemon

import (
	"net"
	"os"
	"time"
)

// writeFile is a tiny test helper that mirrors os.WriteFile but with
// permissive perms. Used in TestSocket_StaleFileGetsCleanedOnStart to
// plant a stale file at the socket path.
func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}

// dial returns a unix-socket connection for tests that bypass the
// Client wrapper (e.g. to send raw bad-JSON payloads).
func dial(path string) (net.Conn, error) {
	return net.DialTimeout("unix", path, 2*time.Second)
}
