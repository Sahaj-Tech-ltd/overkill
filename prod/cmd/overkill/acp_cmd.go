package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/overkill/internal/acp"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/db"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
)

var acpCmd = &cobra.Command{
	Use:   "acp",
	Short: "Agent Communication Protocol — let other agents send messages to overkill",
}

var acpServeCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the ACP server in the foreground",
	RunE: func(cmd *cobra.Command, args []string) error {
		connString := os.Getenv("DATABASE_URL")
		if connString == "" && cfg != nil {
			connString = cfg.DatabaseURL
		}
		if connString == "" {
			return fmt.Errorf("DATABASE_URL required — set it in ~/.overkill/config.toml or the environment")
		}

		database, err := db.Open(connString)
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer database.Close()

		if err := db.Migrate(database); err != nil {
			return fmt.Errorf("migrate database: %w", err)
		}

		store := session.NewPostgresStore(database)

		addr := config.DefaultACPAddr
		if cfg != nil && cfg.ACP.Listen != "" {
			addr = cfg.ACP.Listen
		}
		token, err := loadOrCreateACPToken()
		if err != nil {
			return fmt.Errorf("acp token: %w", err)
		}

		var allowedOrigins []string
		if cfg != nil {
			allowedOrigins = cfg.ACP.AllowedOrigins
		}

		srv := acp.NewServer(acp.Config{
			Addr:           addr,
			Token:          token,
			AllowedOrigins: allowedOrigins,
			Store:          store,
			Name:           "overkill",
			Version:        Version,
		})

		if err := srv.Start(); err != nil {
			return fmt.Errorf("start ACP server: %w", err)
		}
		fmt.Printf("ACP server listening on %s\n", srv.Addr())

		// Wait for shutdown signal.
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		fmt.Fprintln(os.Stderr, "\nshutting down...")
		return srv.Shutdown(context.Background())
	},
}

var acpTokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Print the bearer token (creates one if missing)",
	RunE: func(cmd *cobra.Command, args []string) error {
		tk, err := loadOrCreateACPToken()
		if err != nil {
			return err
		}
		fmt.Println(tk)
		return nil
	},
}

var acpPingCmd = &cobra.Command{
	Use:   "ping <url>",
	Short: "Verify a remote ACP server is up",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		tk, err := loadOrCreateACPToken()
		if err != nil {
			return fmt.Errorf("acp token: %w", err)
		}
		c := acp.NewClient(args[0], tk)
		info, err := c.GetInfo(context.Background())
		if err != nil {
			return err
		}
		fmt.Printf("ok: %s %s\n", info.Name, info.Version)
		return nil
	},
}

func loadOrCreateACPToken() (string, error) {
	if cfg != nil && cfg.ACP.Token != "" {
		return cfg.ACP.Token, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(home, ".overkill", "acp-token")
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		return string(data), nil
	}
	tk := acp.GenerateToken()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(tk), 0o600); err != nil {
		return "", err
	}
	return tk, nil
}

func init() {
	acpCmd.AddCommand(acpServeCmd, acpTokenCmd, acpPingCmd)
	rootCmd.AddCommand(acpCmd)
}
