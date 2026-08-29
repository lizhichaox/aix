package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/lizhichaox/aix/internal"
	"github.com/spf13/cobra"
)

// runCodexProvider implements "aix codex <provider> [model] [effort]": with no provider
// it lists the registered Responses providers, with a provider it switches Codex
// to that provider's default model, and an optional second argument selects an
// explicit model. --list shows the provider's models and defaults.
// Fixed subcommands (restart/restore) are matched by cobra before this runs.
func runCodexProvider(cmd *cobra.Command, args []string) error {
	if err := validateHarnessAuxiliaryFlags(codexListFlag, codexEditFlag, codexDoctorFlag, len(args), codexEditorFlag); err != nil {
		return err
	}
	providerArg := ""
	if len(args) > 0 {
		providerArg = args[0]
	}
	if codexEditFlag {
		return editHarnessRegistry(internal.HarnessCodex, providerArg, codexEditorFlag)
	}
	if codexDoctorFlag {
		return runHarnessDoctor(internal.HarnessCodex, providerArg)
	}
	if len(args) == 0 {
		fmt.Print(codexProviderOverview())
		return nil
	}
	provider := args[0]
	if codexListFlag {
		return showHarnessProviderMapping(internal.HarnessCodex, provider)
	}
	spec, ok := internal.NativeProvider(provider)
	if !ok {
		return fmt.Errorf("unknown Codex Responses provider %q (available: %s)", provider, strings.Join(nativeProviderIDs(), ", "))
	}
	model, effort := "", codexEffortFlag
	if len(args) >= 2 {
		model = args[1]
	}
	if len(args) == 3 {
		if effort != "" {
			return fmt.Errorf("effort specified both positionally and with --effort")
		}
		effort = args[2]
	}
	return switchCodexProvider(spec, model, effort)
}

// switchCodexProvider applies a managed Codex provider switch. An empty model
// resolves to the provider's default; DeepSeek's catalog metadata is then
// best-effort refreshed from the official setup script.
func switchCodexProvider(spec internal.NativeProviderSpec, model, effort string) error {
	app, err := internal.ResolveHarness("codex")
	if err != nil {
		return err
	}
	if err := internal.EnsureDirs(); err != nil {
		return err
	}
	// Validate the model and key before touching the host. Quitting the app is
	// expensive and disruptive, so it must only happen for a switch that will
	// actually succeed; a missing key or bad model should fail immediately.
	selection, err := internal.ResolveHarnessSelection(internal.HarnessCodex, spec.ID, model, effort)
	if err != nil {
		return err
	}
	key, keySource := internal.NativeProviderAPIKey(spec.ID)
	if key == "" {
		return fmt.Errorf("%s API key not found; set $%s or add an auth_token for %q to proxy.toml", spec.Name, spec.EnvKey, spec.Name)
	}
	// Quit the host before mutating. ChatGPT flushes its in-memory config and
	// thread index while quitting, so writing first would let that flush
	// overwrite the provider settings and the history retag we are about to
	// apply. Quitting first (mirroring restoreCodexNative) means the host loads
	// the freshly retagged threads on launch.
	appName := app.ClientAppName()
	if appName != "" {
		fmt.Printf("Quitting %s... ", appName)
		if err := quitMacApp(appName); err != nil {
			return err
		}
		fmt.Println("done")
	}
	// Apply the resolved model, not the raw argument. An empty model resolves to
	// the provider's current default; passing the raw value would let a stale
	// provider template (e.g. one carrying the previous default) leak through.
	if err := internal.ApplyProviderWithSelection(app, spec.ID, selection.ClientModel, selection.Effort); err != nil {
		return err
	}
	if err := internal.SaveAppState("codex", spec.ID); err != nil {
		return fmt.Errorf("save state: %w", err)
	}
	if err := ensureAIXGateway(); err != nil {
		return err
	}
	if err := internal.AppendSwitchLog(internal.HarnessCodex, spec.ID, selection.ClientModel, selection.Effort); err != nil {
		fmt.Printf("  ⚠ switch log not written: %v\n", err)
	}
	fmt.Printf("✓ Codex → %s, effort %s (%s Responses API via AIX gateway", selection.ClientModel, selection.Effort, spec.Name)
	if keySource != "" {
		fmt.Printf("; key from %s", keySource)
	}
	fmt.Println(")")
	fmt.Println("  Protocol is passed through without translation.")
	if spec.ID == "deepseek" && os.Getenv("AIX_SKIP_CATALOG_REFRESH") == "" {
		internal.RefreshDeepSeekCatalog()
	}
	if spec.ID == "opencode-go" && os.Getenv("AIX_SKIP_CATALOG_REFRESH") == "" {
		if count, syncErr := internal.SyncLiveModelCatalog(spec.ID); syncErr != nil {
			fmt.Printf("  ⚠ live catalog sync skipped: %v (using verified built-in models)\n", syncErr)
		} else {
			fmt.Printf("  ✓ live catalog synced (%d verified Codex-compatible models)\n", count)
		}
	}
	syncHistoryAfterSwitch(spec.ID)
	if appName != "" {
		fmt.Printf("Launching %s... ", appName)
		if err := launchMacApp(appName); err != nil {
			return fmt.Errorf("launch %s: %v", appName, err)
		}
		fmt.Println("done")
		fmt.Printf("✓ %s restarted\n", appName)
	}
	return nil
}

// defaultCodexProvider is the provider id Codex records for sessions created
// in its default (GPT) mode; used when syncing history after `aix codex
// restore`.
const defaultCodexProvider = "openai"

// syncHistoryAfterSwitch retags every Codex conversation thread to the given
// provider after a successful provider switch, so the desktop sidebar keeps
// showing the full history under the new provider (openai/codex#31625).
// Best-effort: a failure is reported but never fails the switch.
func syncHistoryAfterSwitch(provider string) {
	res, err := internal.SyncCodexHistory(provider)
	if err != nil {
		fmt.Printf("  ⚠  history sync skipped: %v\n", err)
		return
	}
	if res.Retagged == 0 {
		fmt.Printf("  ✓ conversation history already on %q\n", res.Target)
		return
	}
	fmt.Printf("  ✓ conversation history synced to %q (%d threads; backups in %s)\n", res.Target, res.Retagged, res.BackupDir)
	if !res.DBUpdated && res.DBErr != "" {
		fmt.Printf("  ⚠  %s (rollouts still synced; restart rebuilds the app index)\n", res.DBErr)
	}
}

// codexProviderOverview renders the bare "aix codex" provider listing: every
// registered Responses provider routed through the private AIX gateway.
func codexProviderOverview() string {
	var b strings.Builder
	active := activeCodexProviderID()
	fmt.Fprintf(&b, "Codex supports these Responses providers through the private AIX gateway:\n\n")

	for _, id := range nativeProviderIDs() {
		spec, _ := internal.NativeProvider(id)
		marker := ""
		if id == active {
			marker = "  (active)"
		}
		fmt.Fprintf(&b, "    %-14s %s — default %s%s\n", id, spec.Name, spec.DefaultModel, marker)
	}

	fmt.Fprintf(&b, "\nUsage:\n")
	fmt.Fprintf(&b, "  aix codex <provider>            switch to the provider's default model\n")
	fmt.Fprintf(&b, "  aix codex <provider> <model>    switch to an explicit model\n")
	fmt.Fprintf(&b, "  aix codex <provider> --list     list the provider's models and defaults\n")
	fmt.Fprintf(&b, "  aix codex restore               restore Codex's default native API\n")
	fmt.Fprintf(&b, "  aix codex restart               restart the Codex desktop app\n")
	return b.String()
}

// codexProviderCompletion completes provider IDs for the first argument and
// the provider's models for the second.
func codexProviderCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return nativeProviderIDs(), cobra.ShellCompDirectiveNoFileComp
	}
	if len(args) == 1 && internal.IsNativeProvider(args[0]) {
		if spec, ok := internal.HarnessProvider(internal.HarnessCodex, args[0]); ok {
			ids := make([]string, 0, len(spec.Models))
			for id := range spec.Models {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			return ids, cobra.ShellCompDirectiveNoFileComp
		}
	}
	if len(args) >= 2 {
		return internal.HarnessEfforts(internal.HarnessCodex), cobra.ShellCompDirectiveNoFileComp
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

// nativeProviderIDs returns the sorted IDs of all registered Codex native
// providers.
func nativeProviderIDs() []string {
	registry, err := internal.LoadHarnessRegistry()
	if err != nil {
		return nil
	}
	ids := make([]string, 0, len(registry.Providers))
	for id, provider := range registry.Providers {
		if _, mapped := provider.Harnesses[internal.HarnessCodex]; mapped && internal.IsNativeProvider(id) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// activeCodexProviderID returns the model_provider currently configured in
// ~/.codex, or "" when Codex is not on a custom provider.
func activeCodexProviderID() string {
	var config map[string]interface{}
	if _, err := toml.DecodeFile(internal.CodexConfigPath(), &config); err != nil {
		return ""
	}
	p, _ := config["model_provider"].(string)
	return p
}

var codexRestoreCmd = &cobra.Command{
	Use:   "restore",
	Short: "Restore Codex's default native API",
	Long:  "Remove AIX-managed third-party Codex provider settings and restore Codex's default native API.",
	Args:  cobra.NoArgs,
	RunE:  runCodexRestore,
}

func runCodexRestore(cmd *cobra.Command, args []string) error {
	app, err := internal.ResolveHarness("codex")
	if err != nil {
		return err
	}
	if err := restoreCodexNative(app); err != nil {
		return err
	}
	fmt.Println("✓ Codex restored to its default native API")
	return nil
}
