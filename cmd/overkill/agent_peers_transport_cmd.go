package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/coordination"
	"github.com/spf13/cobra"
)

func agentPeerTransportCommands() []*cobra.Command {
	var result []*cobra.Command
	for _, clear := range []bool{false, true} {
		var sessionID, endpoint, credential, appEndpoint, appTokenFile string
		use, method := "bind-acp", "coordination.transport.bind"
		if clear {
			use, method = "clear-acp", "coordination.transport.clear"
		}
		command := &cobra.Command{Use: use, Short: "Explicitly bind/clear this chat's same-hub ACP tools under application admission", RunE: func(cmd *cobra.Command, _ []string) error {
			remotes, err := configuredACPPeers([]config.ACPPeerConfig{{Name: "app", URL: appEndpoint, TokenFile: appTokenFile}})
			if err != nil {
				return err
			}
			app := remotes["app"]
			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()
			params := map[string]any{"session_id": sessionID}
			if !clear {
				params["endpoint"] = endpoint
				params["credential_file"] = credential
			}
			raw, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
			if err != nil {
				return fmt.Errorf("invalid transport binding")
			}
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(app.URL, "/")+"/rpc", bytes.NewReader(raw))
			if err != nil {
				return fmt.Errorf("invalid application endpoint")
			}
			request.Header.Set("Authorization", "Bearer "+app.Token)
			request.Header.Set("Content-Type", "application/json")
			client, err := coordination.NewPeerHTTPClient(app.URL)
			if err != nil {
				return err
			}
			response, err := client.Do(request)
			if err != nil {
				return fmt.Errorf("application transport binding failed")
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				return fmt.Errorf("application rejected transport binding: HTTP %d", response.StatusCode)
			}
			data, err := io.ReadAll(io.LimitReader(response.Body, 1024*1024+1))
			if err != nil || len(data) > 1024*1024 {
				return fmt.Errorf("invalid application response")
			}
			var envelope struct {
				Result json.RawMessage `json:"result"`
				Error  *struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if json.Unmarshal(data, &envelope) != nil {
				return fmt.Errorf("invalid application response")
			}
			if envelope.Error != nil {
				return fmt.Errorf("application rejected transport binding: %s", envelope.Error.Message)
			}
			if len(envelope.Result) == 0 {
				return fmt.Errorf("application omitted binding result")
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), string(envelope.Result))
			return err
		}}
		command.Flags().StringVar(&sessionID, "session", "", "Existing registered local chat")
		command.Flags().StringVar(&appEndpoint, "app-endpoint", "", "Running application's authenticated API endpoint")
		command.Flags().StringVar(&appTokenFile, "app-token-file", "", "Private application API credential file")
		for _, name := range []string{"session", "app-endpoint", "app-token-file"} {
			_ = command.MarkFlagRequired(name)
		}
		if !clear {
			command.Flags().StringVar(&endpoint, "endpoint", "", "Explicit same-hub ACP endpoint")
			command.Flags().StringVar(&credential, "credential-file", "", "Private scoped credential for this exact local peer")
			_ = command.MarkFlagRequired("endpoint")
			_ = command.MarkFlagRequired("credential-file")
		}
		result = append(result, command)
	}
	return result
}
