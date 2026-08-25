package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/lizhichaox/aix/internal"
	"github.com/spf13/cobra"
)

var (
	claudeListFlag   bool
	claudeEffortFlag string
	claudeEditFlag   bool
	claudeDoctorFlag bool
)

func claudeRunE(cmd *cobra.Command, args []string) error {
	if err := validateHarnessAuxiliaryFlags(claudeListFlag, claudeEditFlag, claudeDoctorFlag, len(args)); err != nil {
		return err
	}
	provider := ""
	if len(args) > 0 {
		provider = args[0]
	}
	if claudeEditFlag {
		return editHarnessRegistry(internal.HarnessClaude, provider)
	}
	if claudeDoctorFlag {
		return runHarnessDoctor(internal.HarnessClaude, provider)
	}
	if len(args) == 0 {
		fmt.Print(claudeOverview())
		return nil
	}
	if claudeListFlag {
		return showHarnessProviderMapping(internal.HarnessClaude, provider)
	}
	model, effort := "", claudeEffortFlag
	if len(args) >= 2 {
		model = args[1]
	}
	if len(args) == 3 {
		if effort != "" {
			return fmt.Errorf("effort specified both positionally and with --effort")
		}
		effort = args[2]
	}
	return switchClaudeProvider(provider, model, effort)
}

func addClaudeFlags(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&claudeListFlag, "list", false, "show the provider's model mapping")
	cmd.Flags().StringVar(&claudeEffortFlag, "effort", "", "reasoning effort (uses the provider default when omitted)")
	cmd.Flags().BoolVar(&claudeEditFlag, "edit", false, "edit provider/model/effort mappings")
	cmd.Flags().BoolVar(&claudeDoctorFlag, "doctor", false, "validate provider/model/effort mappings")
}

func claudeProviderCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	switch len(args) {
	case 0:
		return claudeProviderIDs(), cobra.ShellCompDirectiveNoFileComp
	case 1:
		return claudeProviderModelCompletion(args[0]), cobra.ShellCompDirectiveNoFileComp
	case 2:
		return internal.HarnessEfforts(internal.HarnessClaude), cobra.ShellCompDirectiveNoFileComp
	default:
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
}

func claudeProviderModelCompletion(provider string) []string {
	spec, ok := internal.HarnessProvider(internal.HarnessClaude, provider)
	if !ok {
		return nil
	}
	ids := make([]string, 0, len(spec.Models))
	for id := range spec.Models {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func claudeProviderIDs() []string {
	registry, _ := internal.LoadHarnessRegistry()
	seen := make(map[string]bool, len(registry.Providers))
	for id, provider := range registry.Providers {
		if _, ok := provider.Harnesses[internal.HarnessClaude]; ok {
			seen[id] = true
		}
	}

	// Existing custom providers remain usable only when both Claude clients
	// have a template, preserving the all-or-nothing harness contract.
	codeProviders, _ := internal.ListProviders("claudecode")
	desktopProviders, _ := internal.ListProviders("desktop")
	desktopSet := make(map[string]bool, len(desktopProviders))
	for _, id := range desktopProviders {
		desktopSet[id] = true
	}
	for _, id := range codeProviders {
		if desktopSet[id] {
			seen[id] = true
		}
	}

	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func validClaudeProvider(provider string) bool {
	for _, id := range claudeProviderIDs() {
		if id == provider {
			return true
		}
	}
	return false
}

func activeClaudeProvider(appID string) string {
	state, _ := internal.LoadState()
	provider := state.Apps[appID]
	if provider == "" || provider == "-" {
		return ""
	}
	app, err := internal.ResolveHarness(appID)
	if err != nil {
		return provider
	}
	mode, _, _ := app.StatusMode()
	if mode == "" || mode == "native" {
		return ""
	}
	return provider
}

func claudeOverview() string {
	var b strings.Builder
	code := activeClaudeProvider("claudecode")
	desktop := activeClaudeProvider("desktop")
	fmt.Fprintln(&b, "Claude harness — switches always apply to Code + Desktop")
	if code == desktop && code != "" {
		fmt.Fprintf(&b, "  Current: %s\n", code)
	} else if code != "" || desktop != "" {
		fmt.Fprintf(&b, "  Current: Code=%s, Desktop=%s\n", emptyAsNative(code), emptyAsNative(desktop))
	} else {
		fmt.Fprintln(&b, "  Current: native")
	}
	fmt.Fprintln(&b, "  Providers:")
	for _, id := range claudeProviderIDs() {
		marker := ""
		if id == code && id == desktop {
			marker = " (active)"
		}
		fmt.Fprintf(&b, "    %-14s %s%s\n", id, claudeProviderDisplayName(id), marker)
	}
	fmt.Fprintln(&b, "\nUsage:")
	fmt.Fprintln(&b, "  aix claude <provider> [model] [effort]")
	fmt.Fprintln(&b, "  aix claude <provider> --list|--edit|--doctor")
	fmt.Fprintln(&b, "  aix claude restore|restart")
	return b.String()
}

func emptyAsNative(value string) string {
	if value == "" {
		return "native"
	}
	return value
}

// switchClaudeProvider applies one selection to both Claude clients.
func switchClaudeProvider(provider, model, effort string) error {
	if !validClaudeProvider(provider) {
		return fmt.Errorf("unknown Claude provider %q (available: %s)", provider, strings.Join(claudeProviderIDs(), ", "))
	}
	ids := []string{"claudecode", "desktop"}
	preset, isPreset := internal.KnownProviders()[provider]
	var selection internal.HarnessSelection
	if isPreset && preset.AnthropicNative {
		var err error
		selection, err = internal.ResolveHarnessSelection(internal.HarnessClaude, provider, model, effort)
		if err != nil {
			return err
		}
	}

	if provider == "deepseek" {
		model = selection.UpstreamModel
		if !internal.ValidDeepSeekUpstreamModel(model) {
			return fmt.Errorf("invalid DeepSeek model id %q (expected deepseek-*)", model)
		}
		if err := internal.EnsureClaudeProxyProvider(provider); err != nil {
			return fmt.Errorf("prepare Claude provider: %w", err)
		}
		if err := internal.ApplyDeepSeekClaudeCodeWithEffort(model, selection.Effort); err != nil {
			return fmt.Errorf("apply Claude Code: %w", err)
		}
		if err := internal.ApplyDeepSeekClaudeDesktop(model); err != nil {
			return fmt.Errorf("apply Claude Desktop: %w", err)
		}
		if err := internal.SetDeepSeekClaudeMappings(model); err != nil {
			return fmt.Errorf("set Claude model mapping: %w", err)
		}
	} else if isPreset && preset.AnthropicNative {
		if err := internal.EnsureClaudeProxyProvider(provider); err != nil {
			return fmt.Errorf("prepare Claude provider: %w", err)
		}
		for _, id := range ids {
			app, err := internal.ResolveHarness(id)
			if err != nil {
				return err
			}
			if err := internal.ApplyClaudeProviderWithModelAndEffort(app, provider, selection.ClientModel, selection.Effort); err != nil {
				return fmt.Errorf("apply %s: %w", app.Name, err)
			}
		}
		if err := internal.SetProviderModelMappings(internal.ClaudeProxyProviderID(provider), map[string]string{selection.ClientModel: selection.UpstreamModel}); err != nil {
			return fmt.Errorf("set Claude model mapping: %w", err)
		}
		model = selection.ClientModel
	} else {
		if model != "" || effort != "" {
			return fmt.Errorf("model and effort overrides require a provider in the Claude harness registry")
		}
		for _, id := range ids {
			app, err := internal.ResolveHarness(id)
			if err != nil {
				return err
			}
			if err := internal.ApplyProvider(app, provider); err != nil {
				return fmt.Errorf("apply %s: %w", app.Name, err)
			}
		}
	}

	for _, id := range ids {
		if err := internal.SaveAppState(id, provider); err != nil {
			return fmt.Errorf("save %s state: %w", id, err)
		}
	}
	if err := ensureAIXGateway(); err != nil {
		return err
	}
	fmt.Printf("✓ Claude Code + Claude Desktop → %s", provider)
	if model != "" {
		fmt.Printf(" (model: %s)", model)
	}
	if selection.Effort != "" {
		fmt.Printf(" (effort: %s)", selection.Effort)
	}
	fmt.Println()
	autoRestartClaudeDesktop()
	return nil
}

func claudeProviderDisplayName(provider string) string {
	if preset, ok := internal.KnownProviders()[provider]; ok {
		return preset.Name
	}
	return provider
}
