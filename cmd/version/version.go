package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// versionCmd shows the current version
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Reference-validator version",
	Long:  `Reference-validator version`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("0.0.1")
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
