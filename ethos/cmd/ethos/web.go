package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/ethos/internal/agent"
	"github.com/Sahaj-Tech-ltd/ethos/internal/providers"
	"github.com/Sahaj-Tech-ltd/ethos/internal/web"
)

var (
	webListen string
	webToken  string
	webNoAuth bool
	webOpen   bool
)

var webCmd = &cobra.Command{
	Use:   "web",
	Short: "Launch the Ethos browser UI",
	Long: `Serve the Ethos browser UI over HTTP. Same agent backend as the TUI;
designed for low-overhead access from a phone or another laptop on the LAN.

Default bind is 127.0.0.1:8420. Use --listen 0.0.0.0:8420 to expose to LAN —
in that case make absolutely sure you keep the bearer token private.`,
	RunE: runWeb,
}

func runWeb(cmd *cobra.Command, args []string) error {
	app := buildTUIApp()
	var sender web.AgentSender
	if app != nil && app.Agent != nil {
		sender = &webAgentAdapter{a: app.Agent}
	}

	token := webToken
	if token == "" && !webNoAuth {
		t, err := loadOrCreateWebToken()
		if err != nil {
			return err
		}
		token = t
	}

	// --no-auth is only safe with a localhost listen.
	if webNoAuth && !isLocalhostListen(webListen) {
		return fmt.Errorf("--no-auth requires a localhost listen address (got %q)", webListen)
	}

	provName := ""
	if cfg != nil {
		if pc, _ := resolveProvider(); pc != nil {
			provName = pc.Name
		}
	}

	// Best-effort catalog load — do not block startup if the network is down.
	var catalog *providers.Catalog
	if cat, err := providers.FetchCatalog(context.Background()); err == nil {
		catalog = cat
	}

	srv := web.NewServer(web.Config{
		Addr:     webListen,
		Token:    token,
		NoAuth:   webNoAuth,
		Agent:    sender,
		Store:    app.Store,
		Catalog:  catalog,
		Provider: provName,
		Version:  Version,
	})
	if err := srv.Start(); err != nil {
		return err
	}

	url := fmt.Sprintf("http://%s/", srv.Addr())
	if token != "" {
		url = fmt.Sprintf("http://%s/?t=%s", srv.Addr(), token)
	}
	fmt.Printf("ethos web listening on %s\n", srv.Addr())
	if token != "" {
		fmt.Printf("token: %s\n", token)
	} else {
		fmt.Println("auth: disabled (--no-auth)")
	}
	fmt.Printf("open:  %s\n", url)

	if webOpen {
		_ = openBrowser(url)
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	return srv.Shutdown(ctx)
}

func loadOrCreateWebToken() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(home, ".ethos", "web-token")
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		return strings.TrimSpace(string(data)), nil
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	tk := hex.EncodeToString(b)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(tk), 0o600); err != nil {
		return "", err
	}
	return tk, nil
}

func isLocalhostListen(addr string) bool {
	if addr == "" {
		return true
	}
	return strings.HasPrefix(addr, "127.") || strings.HasPrefix(addr, "localhost:") || strings.HasPrefix(addr, "[::1]:")
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

// webAgentAdapter trims *agent.Agent down to web.AgentSender. Lives here so
// the web package never imports internal/agent anywhere it doesn't have to.
type webAgentAdapter struct{ a *agent.Agent }

func (x *webAgentAdapter) Stream(ctx context.Context, in string) (<-chan agent.StreamEvent, error) {
	return x.a.Stream(ctx, in)
}
func (x *webAgentAdapter) Model() string          { return x.a.Model() }
func (x *webAgentAdapter) SessionID() string      { return x.a.SessionID() }
func (x *webAgentAdapter) SetSessionID(id string) { x.a.SetSessionID(id) }

func init() {
	webCmd.Flags().StringVar(&webListen, "listen", "127.0.0.1:8420", "bind address")
	webCmd.Flags().StringVar(&webToken, "token", "", "bearer token (default: ~/.ethos/web-token)")
	webCmd.Flags().BoolVar(&webNoAuth, "no-auth", false, "disable bearer auth (localhost only)")
	webCmd.Flags().BoolVar(&webOpen, "open", false, "open the URL in the default browser")
	rootCmd.AddCommand(webCmd)
}
