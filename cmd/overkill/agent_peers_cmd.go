package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/acp"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/coordination"
	"github.com/Sahaj-Tech-ltd/overkill/internal/db"
	"github.com/spf13/cobra"
)

// configurePeerCredential never emits a secret. Exclusive creation prevents an
// existing credential from being replaced. Failed delivery removes only
// the file inode created by this call; the DB credential never commits early.
// An unconfirmed commit retains the private file so its credential is not lost.
func configurePeerCredential(ctx context.Context, store *coordination.Store, anchor, target, endpoint string, caps []string, path string) (coordination.Peer, error) {
	peer, _, err := writePeerCredentialFile(path, func(deliver func(string) error) (coordination.Peer, coordination.CredentialMetadata, error) {
		peer, err := store.ConfigureSessionPeer(ctx, anchor, target, endpoint, caps, deliver)
		return peer, coordination.CredentialMetadata{}, err
	})
	return peer, err
}
func configurePeerCredentialWithTTL(ctx context.Context, store *coordination.Store, anchor, target, endpoint string, caps []string, path string, ttl time.Duration) (coordination.Peer, coordination.CredentialMetadata, error) {
	return writePeerCredentialFile(path, func(deliver func(string) error) (coordination.Peer, coordination.CredentialMetadata, error) {
		return store.ConfigureSessionPeerWithTTL(ctx, anchor, target, endpoint, caps, ttl, deliver)
	})
}
func writePeerCredentialFile(path string, issue func(func(string) error) (coordination.Peer, coordination.CredentialMetadata, error)) (coordination.Peer, coordination.CredentialMetadata, error) {
	if !filepath.IsAbs(path) {
		return coordination.Peer{}, coordination.CredentialMetadata{}, fmt.Errorf("absolute private credential output path required")
	}
	var created os.FileInfo
	peer, metadata, err := issue(func(token string) error {
		f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return fmt.Errorf("cannot create private credential file")
		}
		created, e = f.Stat()
		if e != nil {
			f.Close()
			return e
		}
		_, e = io.WriteString(f, token)
		if e == nil {
			e = f.Sync()
		}
		closeErr := f.Close()
		if e != nil {
			return e
		}
		return closeErr
	})
	if err != nil && created != nil && !errors.Is(err, coordination.ErrPeerCredentialUnconfirmed) {
		if info, e := os.Lstat(path); e == nil && os.SameFile(created, info) {
			_ = os.Remove(path)
		}
	}
	return peer, metadata, err
}
func newAgentPeersCommand() *cobra.Command {
	command := &cobra.Command{Use: "agent-peers", Short: "Configure authenticated project peer ingress"}
	var anchor, target, endpoint, output string
	var caps []string
	var ttl time.Duration
	configure := &cobra.Command{Use: "configure", Short: "Configure an existing registered chat and write a scoped credential privately", RunE: func(cmd *cobra.Command, _ []string) error {
		dsn := os.Getenv("DATABASE_URL")
		if dsn == "" && cfg != nil {
			dsn = cfg.DatabaseURL
		}
		if dsn == "" {
			return fmt.Errorf("DATABASE_URL required")
		}
		database, err := db.Open(dsn)
		if err != nil {
			return fmt.Errorf("open database failed")
		}
		defer database.Close()
		store, err := coordination.NewStore(cmd.Context(), database)
		if err != nil {
			return fmt.Errorf("initialize coordination failed")
		}
		peer, metadata, err := configurePeerCredentialWithTTL(cmd.Context(), store, anchor, target, endpoint, caps, output, ttl)
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
			coordination.Peer
			ExpiresAt *time.Time `json:"credential_expires_at"`
		}{peer, metadata.ExpiresAt})
	}}
	configure.Flags().StringVar(&anchor, "anchor-session", "", "Existing registered session authorizing the project")
	configure.Flags().StringVar(&target, "session", "", "Existing registered target session")
	configure.Flags().StringVar(&endpoint, "endpoint", "", "Advertised ACP ingress endpoint; HTTP requires loopback")
	configure.Flags().StringSliceVar(&caps, "capability", nil, "Advertised supported capabilities (metadata, not authorization)")
	configure.Flags().DurationVar(&ttl, "ttl", coordination.DefaultPeerCredentialTTL, "Scoped credential lifetime (1m..720h, default 24h)")
	configure.Flags().StringVar(&output, "credential-out", "", "New absolute private credential file (must not exist)")
	for _, name := range []string{"anchor-session", "session", "endpoint", "credential-out"} {
		_ = configure.MarkFlagRequired(name)
	}
	var tokenFile, operation, bodyFile, address string
	call := &cobra.Command{Use: "call", Short: "Call scoped coordination ingress with a private credential file", RunE: func(cmd *cobra.Command, _ []string) error {
		// Reuse strict configured destination/private-file admission, including HTTP
		// loopback constraints. Neither credential nor URL enters a model argument.
		remotes, err := configuredACPPeers([]config.ACPPeerConfig{{Name: "target", URL: address, TokenFile: tokenFile}})
		if err != nil {
			return err
		}
		f, err := os.Open(bodyFile)
		if err != nil {
			return fmt.Errorf("cannot read request body file")
		}
		defer f.Close()
		raw, err := io.ReadAll(io.LimitReader(f, 72*1024+1))
		if err != nil || len(raw) > 72*1024 {
			return fmt.Errorf("request body exceeds limit")
		}
		var input acp.CoordinationRequest
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if dec.Decode(&input) != nil || dec.Decode(new(any)) != io.EOF {
			return fmt.Errorf("invalid request body")
		}
		remote := remotes["target"]
		response, err := acp.NewClient(remote.URL, remote.Token).Coordination(cmd.Context(), operation, input)
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(response)
	}}
	call.Flags().StringVar(&address, "endpoint", "", "Explicit trusted ACP endpoint")
	call.Flags().StringVar(&tokenFile, "credential-file", "", "Private scoped peer credential file")
	call.Flags().StringVar(&operation, "operation", "", "peers/send/reply/inbox/claim/status/await/cancel/transition/recover")
	call.Flags().StringVar(&bodyFile, "body-file", "", "Bounded JSON operation body")
	for _, name := range []string{"endpoint", "credential-file", "operation", "body-file"} {
		_ = call.MarkFlagRequired(name)
	}
	command.AddCommand(configure, call, agentPeerRevokeCommand())
	command.AddCommand(agentPeerTransportCommands()...)
	return command
}
func init() { rootCmd.AddCommand(newAgentPeersCommand()) }
