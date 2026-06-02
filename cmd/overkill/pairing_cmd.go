// Package main — pairing CLI commands for DM sender approval (§7.1.7).
package main

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/overkill/internal/security"
)

var pairingCmd = &cobra.Command{
	Use:   "pairing",
	Short: "Manage DM pairing (sender identity verification)",
	Long: `DM pairing gates inbound messages behind one-time codes. Unknown
senders receive a pairing challenge; the owner approves with:

  overkill pairing approve <channel> <code>

Pairing codes expire after 60 minutes. Max 3 pending per account.

Storage: JSON files in ~/.overkill/pairing/`,
}

var pairingApproveCmd = &cobra.Command{
	Use:   "approve <channel> <code>",
	Short: "Approve a pending pairing request",
	Args:  cobra.ExactArgs(2),
	RunE:  runPairingApprove,
}

var pairingListCmd = &cobra.Command{
	Use:   "list",
	Short: "List pending pairing requests and known senders",
	RunE:  runPairingList,
}

var pairingDenyCmd = &cobra.Command{
	Use:   "deny <channel> <sender-id>",
	Short: "Remove a sender from the approved list",
	Args:  cobra.ExactArgs(2),
	RunE:  runPairingDeny,
}

func init() {
	pairingCmd.AddCommand(pairingApproveCmd)
	pairingCmd.AddCommand(pairingListCmd)
	pairingCmd.AddCommand(pairingDenyCmd)
	rootCmd.AddCommand(pairingCmd)
}

func pairingDir() string {
	return filepath.Join(overkillHome(), "pairing")
}

func runPairingApprove(cmd *cobra.Command, args []string) error {
	channel, code := args[0], args[1]
	ps := security.NewPairingStore(pairingDir())
	sender, err := ps.ApproveCode(channel, code)
	if err != nil {
		return fmt.Errorf("pairing approve: %w", err)
	}
	fmt.Printf("✅ Approved sender %s on %s (added at %s)\n",
		sender.ID, sender.Channel, sender.AddedAt.Format("2006-01-02 15:04:05"))
	return nil
}

func runPairingList(cmd *cobra.Command, args []string) error {
	ps := security.NewPairingStore(pairingDir())

	pending, err := ps.ListPending()
	if err != nil {
		return fmt.Errorf("pairing list: %w", err)
	}
	if len(pending) > 0 {
		fmt.Println("⏳ Pending pairing requests:")
		for _, r := range pending {
			fmt.Printf("  %s: code=%s sender=%s (created %s)\n",
				r.Channel, r.Code, r.ID, r.CreatedAt.Format("15:04:05"))
		}
	} else {
		fmt.Println("No pending pairing requests.")
	}

	// Show known senders for common channels
	for _, ch := range []string{"telegram", "discord", "whatsapp"} {
		known, _ := ps.ListKnown(ch)
		if len(known) > 0 {
			fmt.Printf("\n✅ Known senders on %s:\n", ch)
			for _, s := range known {
				fmt.Printf("  %s (added %s by %s)\n",
					s.ID, s.AddedAt.Format("2006-01-02 15:04"), s.AddedBy)
			}
		}
	}
	return nil
}

func runPairingDeny(cmd *cobra.Command, args []string) error {
	channel, senderID := args[0], args[1]
	ps := security.NewPairingStore(pairingDir())
	if err := ps.RemoveSender(channel, senderID, ""); err != nil {
		return fmt.Errorf("pairing deny: %w", err)
	}
	fmt.Printf("🚫 Removed %s from %s approved senders\n", senderID, channel)
	return nil
}
