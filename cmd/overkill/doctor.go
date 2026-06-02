package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/doctor"
	"github.com/Sahaj-Tech-ltd/overkill/internal/doctor/checks"
)

var (
	doctorJSON    bool
	doctorFailOn  string
	doctorNoColor bool
	doctorVerbose bool
	doctorBackup  bool
	doctorRestore string
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Run the full self-test across every Overkill subsystem",
	Long: `doctor exercises every subsystem (config, providers, storage, MCP, LSP,
plugins, sync, ACP, tokenizer, tools, hooks, skills, filesystem, disk, etc.)
and prints a color-coded report. Use --json for machine output and --fail-on
to control the exit code in CI.

Use --backup to dump the database to ~/.overkill/backups/.
Use --restore <file> to restore from a backup.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// ── backup / restore (fast path, no subsystem tests) ──
		if doctorBackup {
			return runBackup(cmd)
		}
		if doctorRestore != "" {
			return runRestore(cmd, doctorRestore)
		}

		configDir := ""
		if resolvedCfgPath != "" {
			configDir = filepath.Dir(resolvedCfgPath)
		} else if d, err := config.ConfigDir(); err == nil {
			configDir = d
		}

		runner := doctor.NewRunner()
		deps := checks.DefaultDeps(cfg, configDir)
		checks.RegisterAll(runner, deps)

		ctx, cancel := context.WithCancel(cmd.Context())
		defer cancel()
		summary := runner.Run(ctx)
		summary.Version = Version

		if doctorJSON {
			data, err := json.MarshalIndent(summary, "", "  ")
			if err != nil {
				return fmt.Errorf("encode json: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(data))
		} else {
			doctor.PrettyPrint(cmd.OutOrStdout(), summary, doctor.PrettyOptions{
				NoColor: doctorNoColor || os.Getenv("NO_COLOR") != "",
				Verbose: doctorVerbose,
			})
		}

		switch doctorFailOn {
		case "warn":
			if summary.Counts.Fail+summary.Counts.Warn > 0 {
				os.Exit(1)
			}
		case "fail", "":
			if summary.Counts.Fail > 0 {
				os.Exit(1)
			}
		}
		return nil
	},
}

func init() {
	doctorCmd.Flags().BoolVar(&doctorJSON, "json", false, "emit JSON instead of pretty output")
	doctorCmd.Flags().StringVar(&doctorFailOn, "fail-on", "fail", "exit non-zero on 'warn' or 'fail' (default 'fail')")
	doctorCmd.Flags().BoolVar(&doctorNoColor, "no-color", false, "disable ANSI colors")
	doctorCmd.Flags().BoolVar(&doctorVerbose, "verbose", false, "show detail/fix on successful checks too")
	doctorCmd.Flags().BoolVar(&doctorBackup, "backup", false, "dump database to ~/.overkill/backups/")
	doctorCmd.Flags().StringVar(&doctorRestore, "restore", "", "restore database from a backup file")
	rootCmd.AddCommand(doctorCmd)
}

// ── backup / restore ──────────────────────────────────────────────────

const maxBackups = 7

func backupDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	return filepath.Join(home, ".overkill", "backups"), nil
}

func dbName() string {
	if cfg != nil && cfg.DatabaseURL != "" {
		// Try to extract dbname from postgres:// URL.
		url := cfg.DatabaseURL
		if idx := strings.LastIndex(url, "/"); idx >= 0 {
			name := url[idx+1:]
			if q := strings.Index(name, "?"); q >= 0 {
				name = name[:q]
			}
			if name != "" {
				return name
			}
		}
	}
	return "overkill"
}

func runBackup(cmd *cobra.Command) error {
	dir, err := backupDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create backup dir: %w", err)
	}

	ts := time.Now().UTC().Format("2006-01-02T150405Z")
	db := dbName()
	filename := filepath.Join(dir, fmt.Sprintf("%s-%s.dump", db, ts))

	pgDump, err := exec.LookPath("pg_dump")
	if err != nil {
		return fmt.Errorf("pg_dump not found in PATH — install postgresql-client: %w", err)
	}

	// Use PGPASSWORD from env or rely on socket auth.
	outFile, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("create backup file: %w", err)
	}
	defer outFile.Close()

	dumpCmd := exec.Command(pgDump, "--no-owner", "--no-acl", "-Fc", "-d", db)
	dumpCmd.Stdout = outFile
	dumpCmd.Stderr = cmd.ErrOrStderr()

	if err := dumpCmd.Run(); err != nil {
		os.Remove(filename)
		return fmt.Errorf("pg_dump failed: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Backed up to %s (%s)\n", filename, humanSize(outFile))

	// Rotate old backups.
	pruneBackups(dir, db)

	return nil
}

func runRestore(cmd *cobra.Command, path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("backup file not found: %s", path)
	}

	pgRestore, err := exec.LookPath("pg_restore")
	if err != nil {
		return fmt.Errorf("pg_restore not found in PATH: %w", err)
	}

	restoreCmd := exec.Command(pgRestore, "--clean", "--if-exists", "--no-owner", "--no-acl", "-d", dbName(), path)
	restoreCmd.Stdout = cmd.OutOrStdout()
	restoreCmd.Stderr = cmd.ErrOrStderr()

	fmt.Fprintf(cmd.OutOrStdout(), "Restoring from %s...\n", path)
	if err := restoreCmd.Run(); err != nil {
		return fmt.Errorf("pg_restore failed: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Restore complete. Restart Overkill to pick up changes.\n")
	return nil
}

func pruneBackups(dir, dbName string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	prefix := dbName + "-"
	var backups []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), prefix) && strings.HasSuffix(e.Name(), ".dump") {
			backups = append(backups, e.Name())
		}
	}

	if len(backups) <= maxBackups {
		return
	}

	sort.Strings(backups)
	toDelete := backups[:len(backups)-maxBackups]
	for _, name := range toDelete {
		os.Remove(filepath.Join(dir, name))
	}
}

func humanSize(f *os.File) string {
	info, err := f.Stat()
	if err != nil {
		return "unknown"
	}
	s := info.Size()
	switch {
	case s >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(s)/(1<<30))
	case s >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(s)/(1<<20))
	case s >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(s)/(1<<10))
	default:
		return fmt.Sprintf("%d B", s)
	}
}
