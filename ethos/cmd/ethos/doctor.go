package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/ethos/internal/config"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Diagnose and fix config issues",
	RunE: func(cmd *cobra.Command, args []string) error {
		fixes, err := config.Doctor()
		if err != nil {
			fmt.Printf("%s✗ unfixable issue: %s%s\n", colorRed, err, colorReset)
			return err
		}

		if len(fixes) == 0 {
			fmt.Printf("%s✓ all checks passed%s\n", colorGreen, colorReset)
			return nil
		}

		fmt.Printf("%sApplied %d fix(es):%s\n", colorGreen, len(fixes), colorReset)
		for _, f := range fixes {
			fmt.Printf("  %s• %s%s\n", colorGreen, f, colorReset)
		}

		warns := cfg.Warnings()
		if len(warns) > 0 {
			fmt.Printf("\n%sRemaining warnings:%s\n", colorYellow, colorReset)
			for _, w := range warns {
				fmt.Printf("  %s• %s%s\n", colorYellow, w, colorReset)
			}
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}
