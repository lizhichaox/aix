package cmd

import (
	"fmt"
	"os"

	"github.com/lizhichaox/aix/internal"
	"github.com/spf13/cobra"
)

var Version = "0.11.6"

var rootCmd = &cobra.Command{
	Use:   "aix",
	Short: "Switch AI providers across harnesses",
	Long: `AIX switches providers, models, and reasoning effort across AI harnesses.

The primary command shape is:
  aix <harness> <provider> [model] [effort]`,
}

func Execute() {
	if os.Getenv(internal.ServiceInstallEnv) == "1" {
		if _, err := internal.InstallService(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if os.Getenv(internal.ProxyServiceEnv) == "1" {
		if err := runInternalProxyService(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	internal.ProxyVersion = Version
	rootCmd.Version = Version
	rootCmd.SetVersionTemplate(`AIX v{{.Version}}

Switch AI providers, models, and reasoning effort across AI harnesses while
keeping your conversations ready to continue across every switch.

Common commands:
  aix setup
  aix status
  aix claude <provider> [model] [effort]
  aix codex <provider> [model] [effort]
  aix usage [provider]
  aix log
`)
	rootCmd.CompletionOptions.DisableDefaultCmd = true
}
