// Package cli wires the Sigil CLI: cobra command tree, viper config, and the
// Bubble Tea UI that runs when sigil is invoked with no subcommand.
package cli

import (
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// Version is overridden at build time via -ldflags "-X github.com/incantery/mako/internal/cli.Version=…".
var Version = "0.0.1-dev"

var cfgFile string

var rootCmd = &cobra.Command{
	Use:   "sigil",
	Short: "A Go-native UI application compiler",
	Long: `Sigil compiles a small, semantic UI language to renderer-agnostic IR.
Authors write Sigil; renderers target HTML, SwiftUI, terminal, … without
the language itself knowing anything about HTML, CSS, or JS.`,
	// SilenceUsage hides cobra's auto usage dump when our own RunE returns an
	// error (e.g. the user just hit ctrl-c in the TUI — no need to lecture).
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		p := tea.NewProgram(newHelpModel())
		_, err := p.Run()
		return err
	},
}

// Execute is the only thing main() calls. Returns the error so the binary
// wrapper decides how to surface it (we exit nonzero on error, message goes
// to stderr).
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "",
		"config file (default: $HOME/.mako/config.yaml or ./sigil.yaml)")

	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(checkCmd)
	rootCmd.AddCommand(describeCmd)
	rootCmd.AddCommand(fmtCmd)
	rootCmd.AddCommand(exploreCmd)
	rootCmd.AddCommand(testCmd)
	rootCmd.AddCommand(vetCmd)
	rootCmd.AddCommand(genCmd)
	rootCmd.AddCommand(shotCmd)
	rootCmd.AddCommand(storiesCmd)
	rootCmd.AddCommand(tokensCmd)
	rootCmd.AddCommand(lspCmd)
}

// initConfig hooks viper into the cobra lifecycle. We look in $HOME/.mako
// and the working directory; env vars under SIGIL_* override file values.
// Nothing reads config yet — this is the scaffold real commands will use.
func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		viper.SetConfigName("sigil")
		viper.SetConfigType("yaml")
		viper.AddConfigPath(".")
		if home, err := os.UserHomeDir(); err == nil {
			viper.AddConfigPath(filepath.Join(home, ".mako"))
		}
	}
	viper.SetEnvPrefix("SIGIL")
	viper.AutomaticEnv()
	// Missing config is not an error — config is optional.
	_ = viper.ReadInConfig()
}
