package version

import (
	"fmt"

	"github.com/spf13/cobra"
)

// NewCmdVersion shows the current version.
func NewCmdVersion() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Reference-validator current version",
		Long:  `Reference-validator current version`,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("0.0.1")
		},
	}

	return cmd
}
