package cmd

import (
	"fmt"
	"os"
	"slices"
	"strings"

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
		if err := applySetupHarnessSelections(configuredProviders); err != nil {
			return err
		}
	}

	fmt.Println("\nSetup complete.")
	return nil
}

// applySetupHarnessSelections applies configuration through the same validated
// switch paths as explicit CLI commands. A sole configured provider is
// unambiguous. With multiple credentials, setup only reapplies an already
// selected provider and never invents a new user preference.
func applySetupHarnessSelections(configured []string) error {
	state, err := internal.LoadState()
	if err != nil {
		return fmt.Errorf("load current harness state: %w", err)
	}
	claudeProvider := setupClaudeProvider(configured, state)
	codexProvider := setupHarnessProvider(configured, state.Apps["codex"])
	if claudeProvider == "" && codexProvider == "" && len(configured) > 1 {
		fmt.Printf("\n○ Multiple providers configured (%s); native harness selections were preserved.\n", strings.Join(configured, ", "))
		fmt.Println("  Select explicitly with aix claude <provider> and aix codex <provider>.")
		return nil
	}
	if claudeProvider != "" {
		fmt.Printf("\nApplying Claude harness provider %s...\n", claudeProvider)
		if err := switchClaudeProvider(claudeProvider, "", ""); err != nil {
			return fmt.Errorf("apply Claude setup selection: %w", err)
		}
	}
	if codexProvider != "" {
		spec, ok := internal.NativeProvider(codexProvider)
		if !ok {
			return fmt.Errorf("configured provider %q is not available to Codex", codexProvider)
		}
		fmt.Printf("\nApplying Codex harness provider %s...\n", codexProvider)
		if err := switchCodexProvider(spec, "", ""); err != nil {
			return fmt.Errorf("apply Codex setup selection: %w", err)
		}
	}
	return nil
}

func setupClaudeProvider(configured []string, state *internal.State) string {
	code := setupHarnessProvider(configured, state.Apps["claudecode"])
	desktop := setupHarnessProvider(configured, state.Apps["desktop"])
	if code == desktop {
		return code
	}
	return ""
}

func setupHarnessProvider(configured []string, current string) string {
	if current != "" && current != "-" && slices.Contains(configured, current) {
		return current
	}
	if (current == "" || current == "-") && len(configured) == 1 {
		return configured[0]
	}
	return ""
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
