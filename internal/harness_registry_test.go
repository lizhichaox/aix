package internal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHarnessProviderKeepsProtocolCatalogsSeparate(t *testing.T) {
	codex, ok := HarnessProvider(HarnessCodex, "openrouter")
	if !ok {
		t.Fatal("openrouter Codex harness mapping missing")
	}
	claude, ok := HarnessProvider(HarnessClaude, "openrouter")
	if !ok {
		t.Fatal("openrouter Claude harness mapping missing")
	}
	if codex.APIFormat != APIFormatResponses || claude.APIFormat != APIFormatAnthropic {
		t.Fatalf("API formats = %q/%q", codex.APIFormat, claude.APIFormat)
	}
	if _, ok := codex.Models["anthropic/claude-opus-5"]; ok {
		t.Error("Claude Anthropic model leaked into the Codex Responses catalog")
	}
	if _, ok := claude.Models["anthropic/claude-opus-5"]; !ok {
		t.Error("Claude Anthropic model missing from the Claude catalog")
	}
}

func TestResolveHarnessSelectionDefaultsAndEffort(t *testing.T) {
	for _, harness := range []string{HarnessCodex, HarnessClaude} {
		selection, err := ResolveHarnessSelection(harness, "openrouter", "", "")
		if err != nil {
			t.Fatalf("resolve %s defaults: %v", harness, err)
		}
		if selection.UpstreamModel != "deepseek/deepseek-v4-flash-vision-exp" {
			t.Errorf("%s default upstream = %q", harness, selection.UpstreamModel)
		}
		if selection.Effort != "high" {
			t.Errorf("%s default effort = %q, want high", harness, selection.Effort)
		}
	}

	selection, err := ResolveHarnessSelection(HarnessClaude, "deepseek", DefaultClaudeUpstreamModel, "xhigh")
	if err != nil {
		t.Fatalf("resolve explicit Claude effort: %v", err)
	}
	if selection.Effort != "xhigh" {
		t.Errorf("explicit effort = %q, want xhigh", selection.Effort)
	}
	if _, err := ResolveHarnessSelection(HarnessCodex, "deepseek", "", "xhigh"); err == nil {
		t.Error("Codex mapping should reject an effort absent from its catalog")
	}
}

func TestExplicitModelUsesItsOwnDefaultEffort(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	registry := BundledHarnessRegistry()
	provider := registry.Providers["openrouter"]
	codex := provider.Harnesses[HarnessCodex]
	model := codex.Models["deepseek/deepseek-v4-pro"]
	model.DefaultEffort = "max"
	codex.Models["deepseek/deepseek-v4-pro"] = model
	provider.Harnesses[HarnessCodex] = codex
	registry.Providers["openrouter"] = provider
	if err := WriteHarnessRegistry(HarnessRegistryPath(), registry); err != nil {
		t.Fatal(err)
	}
	selection, err := ResolveHarnessSelection(HarnessCodex, "openrouter", "deepseek/deepseek-v4-pro", "")
	if err != nil {
		t.Fatal(err)
	}
	if selection.Effort != "max" {
		t.Errorf("explicit model effort = %q, want model default max", selection.Effort)
	}
}

func TestEditableHarnessRegistryOverridesDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path, err := EnsureHarnessRegistryFile()
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(home, ".aix", "harnesses.toml") {
		t.Fatalf("registry path = %q", path)
	}
	registry, err := LoadHarnessRegistry()
	if err != nil {
		t.Fatal(err)
	}
	provider := registry.Providers["openrouter"]
	codex := provider.Harnesses[HarnessCodex]
	codex.DefaultModel = "deepseek/deepseek-v4-pro"
	codex.DefaultEffort = "max"
	codex.BaseURL = "https://example.test/responses"
	provider.Harnesses[HarnessCodex] = codex
	registry.Providers["openrouter"] = provider
	if err := WriteHarnessRegistry(path, registry); err != nil {
		t.Fatal(err)
	}

	selection, err := ResolveHarnessSelection(HarnessCodex, "openrouter", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if selection.Model != "deepseek/deepseek-v4-pro" || selection.Effort != "max" {
		t.Fatalf("selection = %+v", selection)
	}
	effective, ok := HarnessProvider(HarnessCodex, "openrouter")
	if !ok || effective.BaseURL != "https://example.test/responses" {
		t.Fatalf("effective mapping = %+v, %v", effective, ok)
	}
}

func TestLegacyHarnessRegistryInheritsBundledContextWindow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	registry := BundledHarnessRegistry()
	provider := registry.Providers["opencode-go"]
	claude := provider.Harnesses[HarnessClaude]
	model := claude.Models[DefaultClaudeUpstreamModel]
	model.ContextWindow = 0
	claude.Models[DefaultClaudeUpstreamModel] = model
	provider.Harnesses[HarnessClaude] = claude
	registry.Providers["opencode-go"] = provider
	if err := EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := WriteHarnessRegistry(HarnessRegistryPath(), registry); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadHarnessRegistry()
	if err != nil {
		t.Fatal(err)
	}
	got := loaded.Providers["opencode-go"].Harnesses[HarnessClaude].Models[DefaultClaudeUpstreamModel].ContextWindow
	if got < oneMillionContext {
		t.Fatalf("legacy context window = %d, want bundled 1M capability", got)
	}
}

func TestVersionOneRegistryAddsOpenCodeGoDeepSeekMessagesModels(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	registry := BundledHarnessRegistry()
	registry.Version = 1
	provider := registry.Providers["opencode-go"]
	claude := provider.Harnesses[HarnessClaude]
	delete(claude.Models, DeepSeekV4FlashModel)
	delete(claude.Models, DeepSeekV4ProModel)
	provider.Harnesses[HarnessClaude] = claude
	registry.Providers["opencode-go"] = provider
	if err := EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := WriteHarnessRegistry(HarnessRegistryPath(), registry); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadHarnessRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Version != 2 {
		t.Fatalf("migrated version = %d, want 2", loaded.Version)
	}
	models := loaded.Providers["opencode-go"].Harnesses[HarnessClaude].Models
	for _, modelID := range []string{DeepSeekV4FlashModel, DeepSeekV4ProModel} {
		if model, ok := models[modelID]; !ok || model.UpstreamModel != modelID {
			t.Errorf("migrated model %q = %+v, %v", modelID, model, ok)
		}
	}
}

func TestHarnessDoctorExplainsInvalidDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	registry := BundledHarnessRegistry()
	provider := registry.Providers["deepseek"]
	claude := provider.Harnesses[HarnessClaude]
	claude.DefaultModel = "missing-model"
	claude.DefaultEffort = "ultra"
	provider.Harnesses[HarnessClaude] = claude
	registry.Providers["deepseek"] = provider
	if err := WriteHarnessRegistry(HarnessRegistryPath(), registry); err != nil {
		t.Fatal(err)
	}
	diagnostics := DiagnoseHarnessRegistry(HarnessClaude, "deepseek")
	wantPaths := map[string]bool{
		"claude/deepseek.default_model":  false,
		"claude/deepseek.default_effort": false,
	}
	for _, diagnostic := range diagnostics {
		if _, ok := wantPaths[diagnostic.Path]; ok && diagnostic.Reason != "" && diagnostic.Suggest != "" {
			wantPaths[diagnostic.Path] = true
		}
	}
	for path, found := range wantPaths {
		if !found {
			t.Errorf("missing actionable diagnostic for %s: %+v", path, diagnostics)
		}
	}
}

func TestBundledHarnessRegistryHasNoDoctorErrors(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, harness := range []string{HarnessCodex, HarnessClaude} {
		for _, diagnostic := range DiagnoseHarnessRegistry(harness, "") {
			if diagnostic.Severity == "error" {
				t.Errorf("bundled %s diagnostic: %+v", harness, diagnostic)
			}
		}
	}
}

func TestClaudeProxyIsDerivedFromEditableHarnessMapping(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENCODE_GO_API_KEY", "sk-go-test")
	if err := EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	registry := BundledHarnessRegistry()
	provider := registry.Providers["opencode-go"]
	claude := provider.Harnesses[HarnessClaude]
	claude.BaseURL = "https://gateway.example.test/anthropic"
	model := claude.Models[DefaultClaudeUpstreamModel]
	model.UpstreamModel = "provider-specific-v4-vision"
	claude.Models[DefaultClaudeUpstreamModel] = model
	provider.Harnesses[HarnessClaude] = claude
	registry.Providers["opencode-go"] = provider
	if err := WriteHarnessRegistry(HarnessRegistryPath(), registry); err != nil {
		t.Fatal(err)
	}
	if err := EnsureClaudeProxyProvider("opencode-go"); err != nil {
		t.Fatal(err)
	}
	proxy, err := LoadProxyConfig()
	if err != nil {
		t.Fatal(err)
	}
	configured := proxy.Providers["opencode-go"]
	if configured == nil || configured.Upstream != claude.BaseURL {
		t.Fatalf("proxy provider = %+v", configured)
	}
	if got := configured.Models[model.ClientModel]; got != "provider-specific-v4-vision" {
		t.Errorf("proxy mapping = %q", got)
	}
	if _, err := os.Stat(HarnessRegistryPath()); err != nil {
		t.Fatal(err)
	}
}
