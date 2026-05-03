package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/ethos/internal/session"
	"github.com/Sahaj-Tech-ltd/ethos/internal/share"
)

var shareCmd = &cobra.Command{
	Use:   "share <sessionID>",
	Short: "Render a session as HTML and upload to a paste service",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		store, err := session.NewBadgerStore(filepath.Join(home, ".ethos", "sessions"))
		if err != nil {
			return fmt.Errorf("share: open store: %w", err)
		}
		defer store.Close()
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
