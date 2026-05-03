package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/ethos/internal/plugin"
)

var pluginCmd = &cobra.Command{
	Use:   "plugin",
	Short: "Manage Ethos plugins",
	Long:  "Discover, install, and inspect plugins under ~/.ethos/plugins/.",
}

var pluginListCmd = &cobra.Command{
	Use:   "list",
	Short: "List discovered plugins",
	RunE: func(cmd *cobra.Command, args []string) error {
		root := plugin.DefaultPluginsDir()
		found, err := plugin.Discover(root)
		if err != nil {
			return err
		}
		if len(found) == 0 {
			fmt.Printf("no plugins found in %s\n", root)
			return nil
		}
		for _, d := range found {
			version := ""
			desc := ""
			if d.StaticManifest != nil {
				version = d.StaticManifest.Version
				desc = d.StaticManifest.Description
			}
			fmt.Printf("%s  %s\n  %s\n  %s\n\n", d.Name, version, d.EntryPath, desc)
		}
		return nil
	},
}

var pluginInstallCmd = &cobra.Command{
	Use:   "install <git-url>",
	Short: "Clone a plugin repo into ~/.ethos/plugins/",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		raw := args[0]
		u, err := url.Parse(raw)
		if err != nil {
			return fmt.Errorf("invalid url: %w", err)
		}
		name := filepath.Base(u.Path)
		name = trimExt(name, ".git")
		if name == "" || name == "." || name == "/" {
			return fmt.Errorf("could not derive plugin name from %s", raw)
		}
		root := plugin.DefaultPluginsDir()
		if err := os.MkdirAll(root, 0o755); err != nil {
			return err
		}
		dest := filepath.Join(root, name)
		if _, err := os.Stat(dest); err == nil {
			return fmt.Errorf("%s already exists; remove it first", dest)
		}
		clone := exec.Command("git", "clone", raw, dest)
		clone.Stdout = cmd.OutOrStdout()
		clone.Stderr = cmd.ErrOrStderr()
		if err := clone.Run(); err != nil {
			return fmt.Errorf("git clone failed: %w", err)
		}
		fmt.Printf("installed plugin to %s\n", dest)
		return nil
	},
}

var pluginRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove an installed plugin",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		dest := filepath.Join(plugin.DefaultPluginsDir(), name)
		if _, err := os.Stat(dest); os.IsNotExist(err) {
			return fmt.Errorf("plugin %q not installed", name)
		}
		if err := os.RemoveAll(dest); err != nil {
			return err
		}
		fmt.Printf("removed %s\n", dest)
		return nil
	},
}

var pluginDoctorCmd = &cobra.Command{
	Use:   "doctor <name>",
	Short: "Run a single plugin in isolation and report handshake errors",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		root := plugin.DefaultPluginsDir()
		found, err := plugin.Discover(root)
		if err != nil {
			return err
		}
		var target *plugin.Discovered
		for i := range found {
			if found[i].Name == name {
				target = &found[i]
				break
			}
		}
		if target == nil {
			return fmt.Errorf("plugin %q not found in %s", name, root)
		}
		bridge := &doctorBridge{}
		client := plugin.NewClient(target.Name, target.EntryPath, target.EntryArgs, target.Env, bridge)
		if target.StaticManifest != nil {
			client.SetStaticManifest(*target.StaticManifest)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := client.Start(ctx); err != nil {
			fmt.Printf("FAIL: %v\n", err)
			return err
		}
		defer client.Shutdown(context.Background())

		m := client.Manifest()
		fmt.Printf("OK: %s v%s\n", m.Name, m.Version)
		if m.Description != "" {
			fmt.Printf("  %s\n", m.Description)
		}
		fmt.Printf("  tools:    %d\n", len(client.Tools()))
		for _, t := range client.Tools() {
			fmt.Printf("    - %s — %s\n", t.Name, t.Description)
		}
		fmt.Printf("  commands: %d\n", len(client.Commands()))
		for _, c := range client.Commands() {
			fmt.Printf("    - %s — %s\n", c.Title, c.Description)
		}
		fmt.Printf("  events:   %v\n", client.SubscribedEvents())
		fmt.Printf("  context:  %v\n", client.HasContextProvider())
		return nil
	},
}

func trimExt(s, ext string) string {
	if len(s) > len(ext) && s[len(s)-len(ext):] == ext {
		return s[:len(s)-len(ext)]
	}
	return s
}

// doctorBridge is a stub HostBridge for `ethos plugin doctor`. It returns
// empty session info and rejects all config reads.
type doctorBridge struct{}

func (doctorBridge) SessionInfo() plugin.SessionInfo { return plugin.SessionInfo{ID: "doctor"} }
func (doctorBridge) ConfigValue(string) (any, bool)  { return nil, false }
func (doctorBridge) Toast(kind, text string)         { fmt.Printf("[toast %s] %s\n", kind, text) }

// Reference json.RawMessage so go imports it (used elsewhere in package).
var _ = json.RawMessage(nil)

func init() {
	pluginCmd.AddCommand(pluginListCmd)
	pluginCmd.AddCommand(pluginInstallCmd)
	pluginCmd.AddCommand(pluginRemoveCmd)
	pluginCmd.AddCommand(pluginDoctorCmd)
	rootCmd.AddCommand(pluginCmd)
}
