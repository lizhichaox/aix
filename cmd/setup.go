package cmd

import (
	"fmt"
	"os"

	"github.com/lizhichaox/aix/internal"
	"github.com/spf13/cobra"
)

var setupProviderOrder = []string{"deepseek", "opencode-go", "opencode-zen", "openrouter"}

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Initialize AIX from configured credentials",
	Long: `Initialize AIX from provider credentials already present in the
environment or AIX configuration, then install the private AIX gateway
service when possible. Missing credentials are reported without prompting.`,
	Args: cobra.NoArgs,
	RunE: runSetup,
}

func runSetup(cmd *cobra.Command, args []string) error {
	fmt.Println("AIX setup")
	fmt.Println("=========")
	if err := internal.EnsureDirs(); err != nil {
		return err
	}
	fmt.Printf("✓ Configuration directory: %s\n\n", internal.AixDir())

	configuredProviders := make([]string, 0, len(setupProviderOrder))
	for _, providerID := range setupProviderOrder {
		preset, ok := internal.KnownProviders()[providerID]
		if !ok {
			continue
		}
		key, source := internal.NativeProviderAPIKey(providerID)
		if key == "" {
			fmt.Printf("○ %-14s not configured (set $%s)\n", providerID, preset.EnvVar)
			continue
		}
		if err := os.Setenv(preset.EnvVar, key); err != nil {
			return err
		}
		if err := internal.EnsureClaudeProxyProvider(providerID); err != nil {
			return fmt.Errorf("configure %s: %w", preset.Name, err)
		}
		if _, _, err := internal.EnsureCodexProxyProvider(providerID, key); err != nil {
			return fmt.Errorf("configure %s Responses route: %w", preset.Name, err)
		}
		fmt.Printf("✓ %-14s credential saved (%s)\n", providerID, source)
		configuredProviders = append(configuredProviders, providerID)
	}

	if len(configuredProviders) == 0 {
		fmt.Println("\n⚠ No provider credentials found.")
		fmt.Println("  Set at least one provider API key, then run aix setup again.")
		fmt.Println("  AIX was initialized, but provider switching will remain unavailable.")
	} else {
		if _, err := internal.InstallService(); err != nil {
			fmt.Printf("\n⚠ AIX gateway service install failed: %v\n", err)
			fmt.Println("  A managed provider switch will start it on demand.")
		} else {
			fmt.Println("\n✓ AIX gateway ready")
		}
		fmt.Println("\n○ Harness selections were left unchanged.")
		fmt.Println("  Switch explicitly with aix claude <provider> or aix codex <provider>.")
	}

	fmt.Println("\nSetup complete.")
	return nil
}

func coreApps() []*internal.HarnessInfo {
	apps := make([]*internal.HarnessInfo, 0, 3)
	for _, id := range []string{"claudecode", "desktop", "codex"} {
		if app, err := internal.ResolveHarness(id); err == nil {
			apps = append(apps, app)
		}
	}
	return apps
}

func init() {
	rootCmd.AddCommand(setupCmd)
}
