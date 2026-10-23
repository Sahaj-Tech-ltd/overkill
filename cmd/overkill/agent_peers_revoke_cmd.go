package main

import (
	"fmt"
	"os"

	"github.com/Sahaj-Tech-ltd/overkill/internal/acp"
	"github.com/Sahaj-Tech-ltd/overkill/internal/coordination"
	"github.com/Sahaj-Tech-ltd/overkill/internal/db"
	"github.com/spf13/cobra"
)

func agentPeerRevokeCommand() *cobra.Command {
	var anchor, target, file string
	command := &cobra.Command{Use: "revoke", Short: "Retire one scoped credential without disabling the peer or its other keys", RunE: func(cmd *cobra.Command, _ []string) error {
		token, err := acp.ReadPeerCredential(file)
		if err != nil {
			return err
		}
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
		if err = store.RevokePeerCredential(cmd.Context(), anchor, target, token); err != nil {
			return fmt.Errorf("scoped credential revocation rejected")
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "Scoped credential revoked; peer identity remains enabled.")
		return err
	}}
	command.Flags().StringVar(&anchor, "anchor-session", "", "Existing registered session authorizing the project")
	command.Flags().StringVar(&target, "session", "", "Existing registered target session")
	command.Flags().StringVar(&file, "credential-file", "", "Private file containing exactly the key to retire")
	for _, flag := range []string{"anchor-session", "session", "credential-file"} {
		_ = command.MarkFlagRequired(flag)
	}
	return command
}
