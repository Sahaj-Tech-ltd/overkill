package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/stretchr/testify/require"
)

func TestConfiguredACPPeersUsePrivateFilesAndFixedDestinations(t *testing.T) {
	file := filepath.Join(t.TempDir(), "peer.token")
	const secret = "configured-private-test-token"
	require.NoError(t, os.WriteFile(file, []byte(secret+"\n"), 0600))
	valid := config.ACPPeerConfig{Name: "backend", URL: "http://127.0.0.1:8421", TokenFile: file}
	peers, err := configuredACPPeers([]config.ACPPeerConfig{valid})
	require.NoError(t, err)
	require.Equal(t, secret, peers["backend"].Token)
	cases := []config.ACPPeerConfig{
		{Name: "backend", URL: "http://remote.invalid", TokenFile: file},
		{Name: "backend", URL: "https://user:password@remote.invalid", TokenFile: file},
		{Name: "backend", URL: "https://remote.invalid?token=value", TokenFile: file},
		{Name: "backend", URL: "https://remote.invalid", TokenFile: "relative"},
		{Name: "invalid/name", URL: valid.URL, TokenFile: file},
	}
	for _, peer := range cases {
		_, err = configuredACPPeers([]config.ACPPeerConfig{peer})
		require.Error(t, err)
		require.NotContains(t, err.Error(), secret)
	}
	_, err = configuredACPPeers([]config.ACPPeerConfig{valid, valid})
	require.ErrorContains(t, err, "duplicate")
	require.NoError(t, os.WriteFile(file, []byte("secret\ninjected"), 0600))
	_, err = configuredACPPeers([]config.ACPPeerConfig{valid})
	require.Error(t, err)
	require.NoError(t, os.WriteFile(file, make([]byte, 4097), 0600))
	_, err = configuredACPPeers([]config.ACPPeerConfig{valid})
	require.Error(t, err)
}
