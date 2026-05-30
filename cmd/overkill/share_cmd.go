package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/share"

	_ "github.com/lib/pq"
)

var shareCmd = &cobra.Command{
	Use:   "share <sessionID>",
	Short: "Render a session as HTML and upload to a paste service",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		connString := os.Getenv("DATABASE_URL")
		if connString == "" && cfg != nil {
			connString = cfg.DatabaseURL
		}
		if connString == "" {
			return fmt.Errorf("share: DATABASE_URL must be set for Postgres backend")
		}
		db, err := sql.Open("postgres", connString)
		if err != nil {
			return fmt.Errorf("share: open postgres: %w", err)
		}
		defer db.Close()
		store := session.NewPostgresStore(db)
		sess, err := store.Load(context.Background(), id)
		if err != nil {
			return fmt.Errorf("share: load session: %w", err)
		}
		html, err := share.Render(sess)
		if err != nil {
			return err
		}
		shareCfg := cfg.Share
		up, err := share.NewUploader(shareCfg)
		if err != nil {
			return err
		}
		url, err := up.Upload(context.Background(), html)
		if err != nil {
			return err
		}
		fmt.Println(url)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(shareCmd)
}
