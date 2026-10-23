//go:build windows

package main

import "io"

func cancellableDesktopInput(input io.ReadCloser) (io.ReadCloser, error) { return input, nil }
