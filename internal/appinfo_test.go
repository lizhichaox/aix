package internal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsLocalProxyURL(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"http://127.0.0.1:2026/v1", true},
		{"http://localhost:2026/v1", true},
		{"http://127.0.0.1", true},
		{"https://api.deepseek.com/v1", false},
		{"https://api.deepseek.com/anthropic", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isLocalProxyURL(c.url); got != c.want {
			t.Errorf("isLocalProxyURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestCodexStatusModeDistinguishesManagedAndNativeResponses(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(CodexConfigPath()), 0755); err != nil {
		t.Fatal(err)
	}
	app, err := ResolveHarness("codex")
	if err != nil {
		t.Fatal(err)
	}
	managed := `model = "gpt-5.6-luna"
model_provider = "opencode-go"

[model_providers.opencode-go]
base_url = "http://127.0.0.1:2026/codex-opencode-go/v1"
wire_api = "responses"
`
	if err := os.WriteFile(CodexConfigPath(), []byte(managed), 0600); err != nil {
		t.Fatal(err)
	}
	if mode, provider, _ := app.StatusMode(); mode != "gateway" || provider != "opencode-go" {
		t.Errorf("managed StatusMode = %q/%q", mode, provider)
	}
	direct := strings.Replace(managed, "http://127.0.0.1:2026/codex-opencode-go/v1", "https://opencode.ai/zen/go/v1", 1)
	if err := os.WriteFile(CodexConfigPath(), []byte(direct), 0600); err != nil {
		t.Fatal(err)
	}
	if mode, provider, _ := app.StatusMode(); mode != "responses" || provider != "opencode-go" {
		t.Errorf("native StatusMode = %q/%q", mode, provider)
	}
}

func TestCurrentHarnessEffort(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(ClaudeSettingsPath()), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ClaudeSettingsPath(), []byte(`{"env":{"CLAUDE_CODE_EFFORT_LEVEL":"xhigh"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := CurrentHarnessEffort(HarnessClaude); got != "xhigh" {
		t.Errorf("Claude effort = %q, want xhigh", got)
	}

	if err := os.MkdirAll(filepath.Dir(CodexConfigPath()), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(CodexConfigPath(), []byte("model_reasoning_effort = \"max\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := CurrentHarnessEffort(HarnessCodex); got != "max" {
		t.Errorf("Codex effort = %q, want max", got)
	}
}

func TestCurrentHarnessModelResolvesManagedAliasesAndContext(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(ClaudeSettingsPath()), 0755); err != nil {
		t.Fatal(err)
	}
	alias, _ := ClaudeDeepSeekAlias(DefaultClaudeUpstreamModel)
	settings := `{"env":{"ANTHROPIC_DEFAULT_SONNET_MODEL":"` + alias + `[1m]"}}`
	if err := os.WriteFile(ClaudeSettingsPath(), []byte(settings), 0600); err != nil {
		t.Fatal(err)
	}
	model, context := CurrentHarnessModel(HarnessClaude, "opencode-go")
	if model != DefaultClaudeUpstreamModel || context != deepSeekV4ContextWindow {
		t.Errorf("Claude model/context = %q/%d, want %q/%d", model, context, DefaultClaudeUpstreamModel, deepSeekV4ContextWindow)
	}

	if err := os.MkdirAll(filepath.Dir(CodexConfigPath()), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(CodexConfigPath(), []byte("model = \"deepseek-v4-pro\"\nmodel_provider = \"opencode-go\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	model, context = CurrentHarnessModel(HarnessCodex, "opencode-go")
	if model != DeepSeekV4ProModel || context != deepSeekV4ContextWindow {
		t.Errorf("Codex model/context = %q/%d, want %q/%d", model, context, DeepSeekV4ProModel, deepSeekV4ContextWindow)
	}
}

func TestCurrentHarnessModelKeepsUnknownNativeModel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(CodexConfigPath()), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(CodexConfigPath(), []byte("model = \"gpt-5.6-sol\"\nmodel_provider = \"openai\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	model, context := CurrentHarnessModel(HarnessCodex, "openai")
	if model != "gpt-5.6-sol" || context != 0 {
		t.Errorf("native model/context = %q/%d, want gpt-5.6-sol/0", model, context)
	}
}

func TestCurrentHarnessModelReadsNativeClaudeTopLevelModel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(ClaudeSettingsPath()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ClaudeSettingsPath(), []byte(`{"model":"sonnet"}`), 0600); err != nil {
		t.Fatal(err)
	}
	model, context := CurrentHarnessModel(HarnessClaude, "anthropic")
	if model != "sonnet" || context != 1_000_000 {
		t.Errorf("native Claude model/context = %q/%d, want sonnet/1000000", model, context)
	}
}

func TestActiveClaudeModelPicksMostRecentSlot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(ClaudeConfigJSONPath()), 0700); err != nil {
		t.Fatal(err)
	}
	cfg := `{
  "clientDataCacheSlots": {
    "bi1-older": {"model": "claude-sonnet-5", "at": 1000},
    "bi1-newest": {"model": "claude-opus-5", "at": 2000}
  }
}`
	if err := os.WriteFile(ClaudeConfigJSONPath(), []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	model, ok := activeClaudeModel()
	if !ok || model != "claude-opus-5" {
		t.Errorf("activeClaudeModel = %q/%v, want claude-opus-5/true", model, ok)
	}
}

func TestCurrentHarnessModelPrefersActiveClaudeSlot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(ClaudeSettingsPath()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ClaudeSettingsPath(), []byte(`{"model":"sonnet"}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := `{
  "clientDataCacheSlots": {
    "bi1-slot": {"model": "claude-opus-5", "at": 2000}
  }
}`
	if err := os.WriteFile(ClaudeConfigJSONPath(), []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	model, context := CurrentHarnessModel(HarnessClaude, "anthropic")
	if model != "claude-opus-5" || context != 1_000_000 {
		t.Errorf("native Claude model/context = %q/%d, want claude-opus-5/1000000", model, context)
	}
}

func TestCurrentHarnessModelFallsBackWhenNoActiveClaudeSlot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(ClaudeSettingsPath()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ClaudeSettingsPath(), []byte(`{"model":"opus"}`), 0600); err != nil {
		t.Fatal(err)
	}
	model, context := CurrentHarnessModel(HarnessClaude, "anthropic")
	if model != "opus" || context != 1_000_000 {
		t.Errorf("native Claude model/context = %q/%d, want opus/1000000", model, context)
	}
}

func TestClaudeNativeModelContextUsesDocumentedFamilies(t *testing.T) {
	cases := map[string]int{
		"sonnet":                    1_000_000,
		"opus":                      1_000_000,
		"claude-sonnet-5":           1_000_000,
		"claude-haiku-4-5-20251001": 200_000,
		"unknown":                   0,
	}
	for model, want := range cases {
		if got := claudeNativeModelContext(model); got != want {
			t.Errorf("claudeNativeModelContext(%q) = %d, want %d", model, got, want)
		}
	}
}

func TestCurrentHarnessModelReadsNativeCodexCatalogContext(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(CodexConfigPath()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(CodexConfigPath(), []byte("model = \"gpt-5.6-sol\"\nmodel_provider = \"openai\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cache := `{"models":[{"slug":"gpt-5.6-sol","context_window":272000}]}`
	if err := os.WriteFile(CodexModelsCachePath(), []byte(cache), 0600); err != nil {
		t.Fatal(err)
	}
	model, context := CurrentHarnessModel(HarnessCodex, "openai")
	if model != "gpt-5.6-sol" || context != 272000 {
		t.Errorf("native Codex model/context = %q/%d, want gpt-5.6-sol/272000", model, context)
	}
}

func TestListProvidersCodexMergesNativeProviders(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(AppDir("codex"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(AppDir("codex"), "custom.toml"), []byte("model = \"x\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	providers, err := ListProviders("codex")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(providers, ",")
	for _, want := range []string{"deepseek", "opencode-zen", "opencode-go", "openrouter"} {
		if !strings.Contains(joined, want) {
			t.Errorf("ListProviders(codex) missing %s: %v", want, providers)
		}
	}
	if strings.Contains(joined, "custom") {
		t.Errorf("ListProviders(codex) should hide non-native templates: %v", providers)
	}
	// Non-Codex apps must not gain Codex-native providers. Claude apps expose
	// only Anthropic-native presets (deepseek, opencode-zen, opencode-go,
	// openrouter), which auto-create their template on switch.
	claudeProviders, err := ListProviders("claudecode")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"deepseek", "opencode-go", "opencode-zen", "openrouter"}
	if len(claudeProviders) != len(want) {
		t.Fatalf("ListProviders(claudecode) = %v, want %v", claudeProviders, want)
	}
	for i, p := range want {
		if claudeProviders[i] != p {
			t.Errorf("ListProviders(claudecode)[%d] = %q, want %q", i, claudeProviders[i], p)
		}
	}
}

func TestExcalidrawProfileBaseURL(t *testing.T) {
	// Resolves through the default text model's provider profile.
	data := map[string]interface{}{
		"aiDefaultTextModel": "deepseek-v4-pro",
		"aiTextModelConfigs": map[string]interface{}{
			"deepseek-v4-pro": map[string]interface{}{
				"providerId": "aix-proxy",
				"model":      "deepseek-v4-pro",
			},
		},
		"aiProviderProfiles": map[string]interface{}{
			"aix-proxy": map[string]interface{}{
				"provider": "openai-compatible",
				"baseURL":  "https://api.deepseek.com/v1",
			},
			"OpenAI": map[string]interface{}{
				"provider": "openai",
				"baseURL":  "https://api.openai.com/v1",
			},
		},
	}
	if got := excalidrawProfileBaseURL(data); got != "https://api.deepseek.com/v1" {
		t.Errorf("excalidrawProfileBaseURL = %q, want %q", got, "https://api.deepseek.com/v1")
	}

	// Falls back to the first profile pointing at the local proxy when the
	// default model's profile cannot be resolved.
	proxyOnly := map[string]interface{}{
		"aiDefaultTextModel": "unknown-model",
		"aiProviderProfiles": map[string]interface{}{
			"aix-proxy": map[string]interface{}{
				"provider": "openai-compatible",
				"baseURL":  "http://127.0.0.1:2026/v1",
			},
		},
	}
	if got := excalidrawProfileBaseURL(proxyOnly); got != "http://127.0.0.1:2026/v1" {
		t.Errorf("excalidrawProfileBaseURL fallback = %q, want %q", got, "http://127.0.0.1:2026/v1")
	}

	if got := excalidrawProfileBaseURL(map[string]interface{}{}); got != "" {
		t.Errorf("excalidrawProfileBaseURL empty = %q, want empty", got)
	}
}
