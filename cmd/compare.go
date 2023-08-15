package cmd

import (
	"fmt"
	"github.com/openshift-kni/reference-validator/util"
	"github.com/spf13/cobra"
)

var (
	ReferenceDir string
	ResourceDir  string
)

// compareCmd represents the compare command
var compareCmd = &cobra.Command{
	Use:   "compare",
	Short: "Compare two sets of k8s resources",
	Long:  `Compare two sets of k8s resources using two directory paths`,
	Args: func(cmd *cobra.Command, args []string) error {
		// Run the custom validation logic
		if util.IsDirectory(ReferenceDir) && util.IsDirectory(ResourceDir) {
			return nil
		}

		return fmt.Errorf("both paths must be a directory")
	},
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("compare called")
	},
}

func init() {
	rootCmd.AddCommand(compareCmd)

	compareCmd.Flags().StringVarP(&ReferenceDir, "reference", "", "", "Reference configuration directory such as source-cr directory from ZTP")
	compareCmd.Flags().StringVarP(&ResourceDir, "resource", "", "", "User configuration directory to read from")
}
