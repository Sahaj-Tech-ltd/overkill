package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/sync"
)

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Push or pull session state to the configured remote backend",
}

var syncPushCmd = &cobra.Command{
	Use:   "push",
	Short: "Push all local sessions to the configured backend",
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, store, err := openSyncManager()
		if err != nil {
			return err
		}
		defer store.Close()
		n, err := mgr.PushAll(context.Background())
		if err != nil {
			return err
		}
		fmt.Printf("pushed %d sessions to %s\n", n, mgr.Backend().Name())
		return nil
	},
}

var syncPullCmd = &cobra.Command{
	Use:   "pull",
	Short: "Pull all remote sessions, merging into the local store",
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, store, err := openSyncManager()
		if err != nil {
			return err
		}
		defer store.Close()
		n, err := mgr.PullAll(context.Background())
		if err != nil {
			return err
		}
		fmt.Printf("pulled %d sessions from %s\n", n, mgr.Backend().Name())
		return nil
	},
}

var syncStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show local vs remote session counts and last sync times",
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, store, err := openSyncManager()
		if err != nil {
			return err
		}
		defer store.Close()
		st, err := mgr.Status(context.Background())
		if err != nil {
			return err
		}
		fmt.Printf("backend:   %s\n", st.Backend)
		fmt.Printf("local:     %d sessions\n", st.Local)
		fmt.Printf("remote:    %d sessions\n", st.Remote)
		fmt.Printf("last push: %s\n", fmtTimeOrNever(st.LastPush.String()))
		fmt.Printf("last pull: %s\n", fmtTimeOrNever(st.LastPull.String()))
		return nil
	},
}

var syncSetupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Interactive setup wizard for the sync backend",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runSyncSetup()
	},
}

func openSyncManager() (*sync.Manager, *session.BadgerStore, error) {
	if cfg == nil {
		return nil, nil, fmt.Errorf("sync: config not loaded")
	}
	if cfg.Sync.Backend == "" {
		return nil, nil, fmt.Errorf("sync: no backend configured (run `overkill sync setup`)")
	}
	be, err := sync.NewBackend(cfg.Sync)
	if err != nil {
		return nil, nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil, err
	}
	dir := filepath.Join(home, ".overkill", "sessions")
	store, err := session.NewBadgerStore(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("sync: open store: %w", err)
	}
	return sync.NewManager(store, be), store, nil
}

func runSyncSetup() error {
	r := bufio.NewReader(os.Stdin)
	fmt.Println("Choose backend: [1] file  [2] s3  [3] git")
	fmt.Print("> ")
	choice, _ := r.ReadString('\n')
	choice = strings.TrimSpace(choice)

	switch choice {
	case "1", "file":
		fmt.Print("Path: ")
		p, _ := r.ReadString('\n')
		cfg.Sync.Backend = "file"
		cfg.Sync.File.Path = strings.TrimSpace(p)
	case "2", "s3":
		cfg.Sync.Backend = "s3"
		cfg.Sync.S3.Endpoint = ask(r, "Endpoint (blank for AWS): ")
		cfg.Sync.S3.Region = ask(r, "Region: ")
		cfg.Sync.S3.Bucket = ask(r, "Bucket: ")
		cfg.Sync.S3.AccessKey = ask(r, "Access key: ")
		cfg.Sync.S3.SecretKey = ask(r, "Secret key: ")
		cfg.Sync.S3.UseSSL = true
	case "3", "git":
		cfg.Sync.Backend = "git"
		cfg.Sync.Git.RemoteURL = ask(r, "Remote URL: ")
		cfg.Sync.Git.Branch = ask(r, "Branch (default main): ")
	default:
		return fmt.Errorf("unknown choice")
	}
	if resolvedCfgPath != "" {
		if err := cfg.Save(resolvedCfgPath); err != nil {
			return err
		}
		fmt.Printf("saved sync config to %s\n", resolvedCfgPath)
	}
	return nil
}

func ask(r *bufio.Reader, prompt string) string {
	fmt.Print(prompt)
	s, _ := r.ReadString('\n')
	return strings.TrimSpace(s)
}

func fmtTimeOrNever(s string) string {
	if strings.HasPrefix(s, "0001-01-01") {
		return "never"
	}
	return s
}

func init() {
	syncCmd.AddCommand(syncPushCmd, syncPullCmd, syncStatusCmd, syncSetupCmd)
	rootCmd.AddCommand(syncCmd)
}
