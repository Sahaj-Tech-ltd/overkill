// Package main — WhatsApp gateway wiring + `overkill whatsapp pair`
// command.
//
// Two pieces glued here:
//
//  1. registerWhatsApp picks a backend per cfg.WhatsApp.Backend and
//     adds it to the gateway hub. Called from gateway_cmd.go.
//
//  2. The whatsapp Cobra subcommand owns the QR-pair flow. We keep
//     it OUT of the daemon path because pairing is a one-time
//     interactive operation, not an always-on service.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mdp/qrterminal/v3"
	"github.com/spf13/cobra"
	whatsmeowclient "go.mau.fi/whatsmeow"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/gateway"
	"github.com/Sahaj-Tech-ltd/overkill/internal/gateway/whatsapp/cloud"
	wameow "github.com/Sahaj-Tech-ltd/overkill/internal/gateway/whatsapp/whatsmeow"
)

// registerWhatsApp wires whichever backend the config asked for.
// Errors are returned (caller logs); we don't fail the whole gateway
// hub over one misconfigured channel.
func registerWhatsApp(hub *gateway.Hub, disp *gateway.Dispatcher, wa config.WhatsAppConfig, logger *log.Logger, nb *notifyBots) error {
	backend := strings.ToLower(strings.TrimSpace(wa.Backend))
	switch backend {
	case "", "whatsmeow":
		// Default to whatsmeow when the user enabled WhatsApp but
		// didn't pick a backend — it's the friction-light personal
		// path.
		storePath := wa.Whatsmeow.StorePath
		if storePath == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("whatsmeow: home dir: %w", err)
			}
			storePath = filepath.Join(home, ".overkill", "whatsapp.db")
		}
		bot := wameow.NewBot(storePath, wa.AllowedFrom, disp)
		bot.Logger = logger
		hub.Add(bot)
		if nb != nil {
			nb.whatsmeowBot = bot
		}
		logger.Printf("whatsapp/whatsmeow: registered (store=%s, %d sender(s) on allow-list)",
			storePath, len(wa.AllowedFrom))
	case "cloud":
		c := wa.Cloud
		// Env fallbacks for the secret-shaped fields. The config
		// file is checked into nothing (gitignored) but we still
		// prefer the env-var path for production deployments.
		if c.AccessToken == "" {
			c.AccessToken = os.Getenv("WHATSAPP_CLOUD_ACCESS_TOKEN")
		}
		if c.AppSecret == "" {
			c.AppSecret = os.Getenv("WHATSAPP_CLOUD_APP_SECRET")
		}
		if c.VerifyToken == "" {
			c.VerifyToken = os.Getenv("WHATSAPP_CLOUD_VERIFY_TOKEN")
		}
		listen := c.Listen
		if listen == "" {
			listen = "127.0.0.1:7798"
		}
		bot := cloud.NewBot(c.PhoneNumberID, c.AccessToken, c.AppSecret, c.VerifyToken, listen, wa.AllowedFrom, disp)
		bot.Logger = logger
		hub.Add(bot)
		if nb != nil {
			nb.whatsappCloudBot = bot
		}
		logger.Printf("whatsapp/cloud: registered (phone_id=%s, listen=%s, %d sender(s) on allow-list)",
			c.PhoneNumberID, listen, len(wa.AllowedFrom))
	default:
		return fmt.Errorf("unknown backend %q (want whatsmeow|cloud)", wa.Backend)
	}
	return nil
}

// whatsappCmd is the umbrella for whatsapp-specific subcommands.
// Today only `pair`; future additions might include `unpair`, status,
// or media-bandwidth tuning.
var whatsappCmd = &cobra.Command{
	Use:   "whatsapp",
	Short: "WhatsApp gateway helpers (whatsmeow pairing)",
}

var whatsappPairCmd = &cobra.Command{
	Use:   "pair",
	Short: "Pair a phone with the whatsmeow backend (one-time QR scan)",
	Long: `Prints a QR code to the terminal. Open WhatsApp on your phone:
Settings → Linked Devices → Link a Device, then scan. The pairing
seeds ~/.overkill/whatsapp.db (or whatever store_path is configured)
so future 'overkill gateway' invocations can connect without the QR
dance.

This is whatsmeow-only. Cloud API doesn't need a pair step; configure
the webhook + access token in your TOML and you're done.`,
	RunE: runWhatsappPair,
}

func init() {
	whatsappCmd.AddCommand(whatsappPairCmd)
	rootCmd.AddCommand(whatsappCmd)
}

func runWhatsappPair(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Resolve store path the same way registerWhatsApp does.
	configPath, _ := config.ConfigPath()
	cfg, _ := config.Load(configPath)
	storePath := ""
	if cfg != nil {
		storePath = cfg.Gateways.WhatsApp.Whatsmeow.StorePath
	}
	if storePath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("whatsapp pair: home dir: %w", err)
		}
		storePath = filepath.Join(home, ".overkill", "whatsapp.db")
	}
	if err := os.MkdirAll(filepath.Dir(storePath), 0o755); err != nil {
		return fmt.Errorf("whatsapp pair: mkdir: %w", err)
	}

	client, err := wameow.OpenClientForPair(ctx, storePath)
	if err != nil {
		return fmt.Errorf("whatsapp pair: open store: %w", err)
	}
	if client.Store.ID != nil {
		fmt.Printf("%s✓ already paired as %s — nothing to do%s\n",
			colorGreen, client.Store.ID, colorReset)
		return nil
	}

	qrChan, _ := client.GetQRChannel(ctx)
	if err := client.Connect(); err != nil {
		return fmt.Errorf("whatsapp pair: connect: %w", err)
	}
	defer client.Disconnect()

	fmt.Printf("%sopen WhatsApp → Settings → Linked Devices → Link a Device → scan this:%s\n\n",
		colorBlue, colorReset)
	for evt := range qrChan {
		switch evt.Event {
		case "code":
			// Re-render the QR on each refresh — WhatsApp rotates
			// the code every ~20s for security.
			qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
		case "success":
			fmt.Printf("\n%s✓ paired as %s%s\n", colorGreen, client.Store.ID, colorReset)
			fmt.Printf("  device store: %s\n", storePath)
			return nil
		case "timeout":
			return errors.New("whatsapp pair: timed out (QR expired before scan)")
		}
	}
	return nil
}

// OpenClientForPair is the exported wrapper for the pair command. We
// re-use the same store-opening logic the Bot uses on Run; whatsmeow
// requires opening the store before generating a QR.
var _ = whatsmeowclient.Client{} // keep the import to surface the version pin
