package cli

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version of sigil",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("sigil %s (%s/%s, go %s)\n",
			Version, runtime.GOOS, runtime.GOARCH, runtime.Version())
	},
}
