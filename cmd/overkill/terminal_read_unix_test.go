//go:build unix

package main

import (
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
	"os"
	"testing"
)

func TestReadEscapeTailRestoresDescriptorFlags(t *testing.T) {
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	defer reader.Close()
	defer writer.Close()
	flags, err := unix.FcntlInt(reader.Fd(), unix.F_GETFL, 0)
	require.NoError(t, err)
	buf := make([]byte, 2)
	n, err := readEscapeTail(reader, buf)
	require.NoError(t, err)
	require.Zero(t, n)
	_, err = writer.Write([]byte("[A"))
	require.NoError(t, err)
	n, err = readEscapeTail(reader, buf)
	require.NoError(t, err)
	require.Equal(t, 2, n)
	require.Equal(t, "[A", string(buf))
	after, err := unix.FcntlInt(reader.Fd(), unix.F_GETFL, 0)
	require.NoError(t, err)
	require.Equal(t, flags, after)
}
