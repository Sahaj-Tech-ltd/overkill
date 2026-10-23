//go:build windows

package main

import "testing"

func TestTwoACPCLIProcessesExchangeCorrelatedWorkspaceWork(t *testing.T) {
	t.Skip("Unix process-group and SIGTERM lifecycle acceptance is unsupported on Windows; Windows process shutdown is not claimed by this test")
}
