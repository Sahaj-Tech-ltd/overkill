package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/ethos/internal/browser"
)

var browserCmd = &cobra.Command{
	Use:   "browser",
	Short: "Headless browser utilities",
}

var browserTestCmd = &cobra.Command{
	Use:   "test <url>",
	Short: "Smoke-test the agentic browser against a URL",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		url := args[0]

		opts := browser.Options{Headless: true}
		if cfg != nil {
			opts.ChromePath = cfg.Browser.ChromePath
			opts.UserAgent = cfg.Browser.UserAgent
		}
		mgr := browser.NewManager(opts)
		defer mgr.Close()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		b, err := mgr.Get(ctx)
		if err != nil {
			return fmt.Errorf("spawn browser: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "spawned chrome (pid=%d)\n", mgr.Status().PID)

		if err := b.Navigate(url); err != nil {
			return fmt.Errorf("navigate: %w", err)
		}
		title, _ := b.Title()
		cur, _ := b.URL()
		png, err := b.Screenshot(1280, 720)
		if err != nil {
			return fmt.Errorf("screenshot: %w", err)
		}

		f, err := os.CreateTemp("", "ethos-browser-*.png")
		if err != nil {
			return err
		}
		_, _ = f.Write(png)
		_ = f.Close()

		fmt.Fprintf(cmd.OutOrStdout(), "title:      %s\nurl:        %s\nscreenshot: %s (%d bytes)\n",
			title, cur, f.Name(), len(png))
		return nil
	},
}

func init() {
	browserCmd.AddCommand(browserTestCmd)
	rootCmd.AddCommand(browserCmd)
}
