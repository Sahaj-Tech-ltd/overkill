package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/ethos/internal/acp"
)

var acpCmd = &cobra.Command{
	Use:   "acp",
	Short: "Agent Communication Protocol — let other agents send messages to ethos",
}

var acpServeCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the ACP server in the foreground",
	RunE: func(cmd *cobra.Command, args []string) error {
		token, err := loadOrCreateACPToken()
		if err != nil {
			return err
		}
		app := buildTUIApp()
		var sender acp.Sender
		if app != nil && app.Agent != nil {
			sender = &acpAgentAdapter{a: app.Agent}
		}
		listen := cfg.ACP.Listen
		if listen == "" {
			listen = "127.0.0.1:8421"
		}
		srv := acp.NewServer(acp.Config{
			Addr:           listen,
			Token:          token,
			AllowedOrigins: cfg.ACP.AllowedOrigins,
			Agent:          sender,
			Store:          app.Store,
			Name:           "ethos",
			Version:        Version,
		})
		if err := srv.Start(); err != nil {
			return err
		}
		fmt.Printf("acp server listening on %s\n", listen)
		fmt.Printf("token: %s\n", token)

		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
		<-stop
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		return srv.Shutdown(ctx)
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
		tk, _ := loadOrCreateACPToken()
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
	path := filepath.Join(home, ".ethos", "acp-token")
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		return string(data), nil
	}
	tk := acp.GenerateToken()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
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
