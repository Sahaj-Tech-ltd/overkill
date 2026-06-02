package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/overkill/internal/db"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
)

var sessionCmd = &cobra.Command{
	Use:   "session",
	Short: "Manage sessions",
}

var sessionListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List sessions",
	Aliases: []string{"ls"},
	RunE:    runSessionList,
}

var sessionShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show session details",
	Args:  cobra.ExactArgs(1),
	RunE:  runSessionShow,
}

var sessionDeleteCmd = &cobra.Command{
	Use:     "delete <id>",
	Short:   "Delete a session",
	Aliases: []string{"rm"},
	Args:    cobra.ExactArgs(1),
	RunE:    runSessionDelete,
}

func openSessionDB() (*session.PostgresStore, error) {
	connString := os.Getenv("DATABASE_URL")
	if cfg != nil && cfg.DatabaseURL != "" {
		connString = cfg.DatabaseURL
	}
	if connString == "" {
		return nil, fmt.Errorf("DATABASE_URL required for Postgres backend — set it in ~/.overkill/config.toml or the environment")
	}

	database, err := db.Open(connString)
	if err != nil {
		return nil, err
	}
	defer database.Close()

	if err := db.Migrate(database); err != nil {
		database.Close()
		return nil, err
	}

	return session.NewPostgresStore(database), nil
}

func runSessionList(cmd *cobra.Command, args []string) error {
	sstore, err := openSessionDB()
	if err != nil {
		return fmt.Errorf("session list: %w", err)
	}
	defer sstore.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sessions, err := sstore.List(ctx, session.ListOptions{})
	if err != nil {
		return fmt.Errorf("session list: %w", err)
	}

	if len(sessions) == 0 {
		fmt.Println("No sessions found.")
		return nil
	}

	fmt.Printf("%s%s%s\n", colorBold, padRight("ID", 12)+"  "+padRight("TITLE", 30)+"  "+padRight("FOLDER", 20)+"  UPDATED", colorReset)
	for _, s := range sessions {
		updated := s.UpdatedAt.Format("2006-01-02 15:04")
		fmt.Printf("%s  %s  %s  %s\n",
			padRight(truncate(s.ID, 12), 12),
			padRight(truncate(s.Title, 30), 30),
			padRight(truncate(s.Folder, 20), 20),
			updated,
		)
	}
	return nil
}

func runSessionShow(cmd *cobra.Command, args []string) error {
	sstore, err := openSessionDB()
	if err != nil {
		return fmt.Errorf("session show: %w", err)
	}
	defer sstore.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sess, err := sstore.Load(ctx, args[0])
	if err != nil {
		return fmt.Errorf("session show: %w", err)
	}
	if sess == nil {
		return fmt.Errorf("session %q not found", args[0])
	}

	fmt.Printf("ID:        %s\n", sess.ID)
	fmt.Printf("Title:     %s\n", sess.Title)
	fmt.Printf("Folder:    %s\n", sess.Folder)
	fmt.Printf("Model:     %s\n", sess.Model)
	fmt.Printf("Provider:  %s\n", sess.Provider)
	fmt.Printf("Status:    %s\n", sess.Status)
	fmt.Printf("Created:   %s\n", sess.CreatedAt.Format(time.RFC3339))
	fmt.Printf("Updated:   %s\n", sess.UpdatedAt.Format(time.RFC3339))
	if sess.ParentID != "" {
		fmt.Printf("Parent:    %s\n", sess.ParentID)
	}
	if len(sess.Children) > 0 {
		fmt.Printf("Children:  %s\n", strings.Join(sess.Children, ", "))
	}
	return nil
}

func runSessionDelete(cmd *cobra.Command, args []string) error {
	sstore, err := openSessionDB()
	if err != nil {
		return fmt.Errorf("session delete: %w", err)
	}
	defer sstore.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := sstore.Delete(ctx, args[0]); err != nil {
		return fmt.Errorf("session delete: %w", err)
	}

	fmt.Printf("%s✓ session %s deleted%s\n", colorGreen, args[0], colorReset)
	return nil
}

func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

func init() {
	sessionCmd.AddCommand(sessionListCmd)
	sessionCmd.AddCommand(sessionShowCmd)
	sessionCmd.AddCommand(sessionDeleteCmd)

	rootCmd.AddCommand(sessionCmd)
}
