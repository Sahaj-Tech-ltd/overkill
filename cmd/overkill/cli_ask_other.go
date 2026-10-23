//go:build !unix

package main

import (
	"context"
	"os"
)

// A cancellable native console reader is not yet implemented on this platform.
// Keep ordinary REPL input, but reject ask_user explicitly instead of blocking
// a model tool call on an uncancellable console read.
const cliTerminalAskSupported = false

func readCLIByte(ctx context.Context, input *os.File, b []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return input.Read(b)
}
