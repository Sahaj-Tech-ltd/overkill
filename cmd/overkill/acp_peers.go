package main

import (
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
)

var acpPeerName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func configuredACPPeers(peers []config.ACPPeerConfig) (map[string]tools.ACPRemote, error) {
	remotes := make(map[string]tools.ACPRemote, len(peers))
	for _, peer := range peers {
		if !acpPeerName.MatchString(peer.Name) {
			return nil, fmt.Errorf("ACP peer requires a simple name of1..64characters")
		}
		if _, ok := remotes[peer.Name]; ok {
			return nil, fmt.Errorf("ACP peer %s: duplicate name", peer.Name)
		}
		endpoint, err := url.Parse(peer.URL)
		if err != nil || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || (endpoint.Scheme != "https" && endpoint.Scheme != "http") {
			return nil, fmt.Errorf("ACP peer %s: absolute HTTP(S) URL without credentials/query/fragment required", peer.Name)
		}
		host := endpoint.Hostname()
		ip := net.ParseIP(host)
		if endpoint.Scheme == "http" && !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
			return nil, fmt.Errorf("ACP peer %s: plain HTTP requires a loopback host", peer.Name)
		}
		if !filepath.IsAbs(peer.TokenFile) {
			return nil, fmt.Errorf("ACP peer %s: absolute token_file required", peer.Name)
		}
		info, err := os.Lstat(peer.TokenFile)
		if err != nil {
			return nil, fmt.Errorf("ACP peer %s: cannot inspect token_file: %w", peer.Name, err)
		}
		if !info.Mode().IsRegular() || info.Size() > 4096 || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
			return nil, fmt.Errorf("ACP peer %s: token_file must be a private regular file of at most4096bytes", peer.Name)
		}
		file, err := os.Open(peer.TokenFile)
		if err != nil {
			return nil, fmt.Errorf("ACP peer %s: cannot open token_file", peer.Name)
		}
		data, readErr := io.ReadAll(io.LimitReader(file, 4097))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || len(data) > 4096 {
			return nil, fmt.Errorf("ACP peer %s: cannot read bounded token_file", peer.Name)
		}
		token := strings.TrimSpace(string(data))
		if token == "" {
			return nil, fmt.Errorf("ACP peer %s: empty credential", peer.Name)
		}
		for _, r := range token {
			if r < 0x21 || r > 0x7e {
				return nil, fmt.Errorf("ACP peer %s: credential must be printable non-whitespace ASCII", peer.Name)
			}
		}
		remotes[peer.Name] = tools.ACPRemote{URL: strings.TrimRight(peer.URL, "/"), Token: token}
	}
	return remotes, nil
}
