package internal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnthropicNativePresets(t *testing.T) {
	presets := KnownProviders()
	for _, id := range []string{"deepseek", "opencode-zen", "opencode-go", "openrouter"} {
		if !presets[id].AnthropicNative {
			t.Errorf("%s preset must be Anthropic-native (Anthropic-compatible upstream)", id)
		}
		if presets[id].AnthropicUpstream == "" {
			t.Errorf("%s preset must declare an Anthropic upstream", id)
		}
	}
	for _, id := range []string{"kimi", "qwen"} {
		if presets[id].AnthropicNative {
			t.Errorf("%s preset must not be Anthropic-native", id)
		}
	}
}

func TestClaudeProviderModels(t *testing.T) {
	for _, id := range []string{"deepseek", "opencode-zen", "opencode-go", "openrouter"} {
		models := ClaudeProviderModels(id)
		if len(models) == 0 {
			t.Errorf("ClaudeProviderModels(%s) empty", id)
			continue
		}
		if _, ok := ClaudeModelFor(id, models[0].Alias); !ok {
			t.Errorf("ClaudeModelFor(%s, %q) should match its own alias", id, models[0].Alias)
		}
		if _, ok := ClaudeModelFor(id, models[0].Upstream); !ok {
			t.Errorf("ClaudeModelFor(%s, %q) should match its own upstream", id, models[0].Upstream)
		}
		for _, m := range models {
			// OpenRouter additionally lists raw DeepSeek vendor/model
			// slugs (identity aliases) for Claude Code; the desktop model
			// guard only accepts the Anthropic-shaped curated aliases,
			// and desktop3pGatewayEntry filters those out.
			if id != "openrouter" && !strings.HasPrefix(m.Alias, "claude-") {
				t.Errorf("%s alias %q must be Anthropic-shaped for the desktop model guard", id, m.Alias)
			}
			if m.DisplayName == "" || m.Upstream == "" {
				t.Errorf("%s model %q has empty display name or upstream", id, m.Alias)
			}
		}
	}
}

func TestOpenRouterDeepSeekModels(t *testing.T) {
	models := ClaudeProviderModels("openrouter")
	seen := make(map[string]bool)
	for _, m := range models {
		if strings.HasPrefix(m.Alias, "deepseek/") || strings.HasPrefix(m.Alias, "~deepseek/") {
			if m.Alias != m.Upstream {
				t.Errorf("deepseek entry %q must be an identity mapping (upstream %q)", m.Alias, m.Upstream)
			}
			seen[m.Alias] = true
		}
	}
	for _, slug := range []string{
		"deepseek/deepseek-chat",
		"deepseek/deepseek-r1",
		"deepseek/deepseek-v3.2",
		"deepseek/deepseek-v4-flash",
		"deepseek/deepseek-v4-flash-0731",
		"deepseek/deepseek-v4-pro",
		"~deepseek/deepseek-v4-flash-latest",
	} {
		if !seen[slug] {
			t.Errorf("openrouter picker missing %s", slug)
		}
	}
	aliases := ClaudeModelAliases("openrouter")
	found := false
	for _, a := range aliases {
		if a == "deepseek/deepseek-v4-flash" {
			found = true
		}
	}
	if !found {
		t.Error("deepseek/deepseek-v4-flash missing from openrouter completion aliases")
	}
}

func TestClaudeModelContextWindows(t *testing.T) {
	for _, m := range ClaudeProviderModels("deepseek") {
		if !m.Supports1M() {
			t.Errorf("deepseek alias %q must declare a 1M context window", m.Alias)
		}
	}
	// OpenCode Go's three DeepSeek V4 models are verified at 1M; its other
	// compatibility models still have unknown windows.
	for _, m := range ClaudeProviderModels("opencode-go") {
		want1M := strings.HasPrefix(m.Upstream, "deepseek-v4-")
		if m.Supports1M() != want1M {
			t.Errorf("opencode-go model %q Supports1M=%v, want %v", m.Alias, m.Supports1M(), want1M)
		}
	}
	// Other Anthropic-native presets proxy upstream models whose context
	// windows are not verified; never advertise a 1M variant for them.
	for _, id := range []string{"opencode-zen", "openrouter"} {
		for _, m := range ClaudeProviderModels(id) {
			if m.Supports1M() {
				t.Errorf("%s model %q must not claim 1M context (upstream window unknown)", id, m.Alias)
			}
		}
	}
}

func TestClaudeCodeModelID(t *testing.T) {
	deepseek, ok := ClaudeModelFor("deepseek", ClaudeCodeDeepSeekModel)
	if !ok {
		t.Fatal("DeepSeek Claude model missing")
	}
	modelID := ClaudeCodeModelID(deepseek)
	if modelID != ClaudeCodeDeepSeekModel+"[1m]" {
		t.Errorf("ClaudeCodeModelID() = %q", modelID)
	}
	resolved, ok := ClaudeModelFor("deepseek", modelID)
	if !ok || resolved.Alias != ClaudeCodeDeepSeekModel {
		t.Errorf("ClaudeModelFor() did not resolve 1M id: %+v, %v", resolved, ok)
	}
}

func TestDynamicDeepSeekClaudeModel(t *testing.T) {
	const model = "deepseek-v4-flash-vision-exp"
	if !ValidDeepSeekUpstreamModel(model) {
		t.Fatal("new DeepSeek model id should be accepted syntactically")
	}
	m, ok := ClaudeModelFor("deepseek", model)
	if !ok {
		t.Fatal("dynamic DeepSeek model should resolve")
	}
	if !strings.HasPrefix(m.Alias, "claude-fable-aix-") {
		t.Errorf("dynamic alias = %q, want claude-fable-aix-*", m.Alias)
	}
	if m.Upstream != model || m.DisplayName != model {
		t.Errorf("dynamic model = %+v", m)
	}
	if !m.Supports1M() {
		t.Error("dynamic DeepSeek model must declare a 1M context window")
	}
	again, _ := ClaudeModelFor("deepseek", model)
	if again.Alias != m.Alias {
		t.Errorf("dynamic alias is not stable: %q != %q", again.Alias, m.Alias)
	}
	if ValidDeepSeekUpstreamModel("gpt-5.5") || ValidDeepSeekUpstreamModel("deepseek-bad model") {
		t.Error("invalid/non-DeepSeek model ids should be rejected")
	}
}

func TestResolveClaudeSwitchModel(t *testing.T) {
	for _, provider := range []string{"deepseek", "opencode-zen", "opencode-go", "openrouter"} {
		defaultModel, err := ResolveClaudeSwitchModel(provider, "")
		if err != nil {
			t.Fatalf("default %s model: %v", provider, err)
		}
		wantUpstream := DefaultClaudeUpstreamModel
		if provider == "openrouter" {
			wantUpstream = "deepseek/" + DefaultClaudeUpstreamModel
		}
		if defaultModel.Upstream != wantUpstream {
			t.Errorf("default %s upstream = %q, want %q", provider, defaultModel.Upstream, wantUpstream)
		}
		spec, ok := ClaudeProviderSpecFor(provider)
		if !ok || spec.DefaultEffort != "high" {
			t.Errorf("default %s effort = %q, want high", provider, spec.DefaultEffort)
		}
	}
	if _, err := ResolveClaudeSwitchModel("opencode-zen", "bogus-model"); err == nil {
		t.Error("unknown opencode-zen model should be rejected")
	}
	// A curated slug resolves to its Claude-shaped alias (desktop-safe).
	m, err := ResolveClaudeSwitchModel("openrouter", "anthropic/claude-sonnet-4.6")
	if err != nil {
		t.Fatalf("openrouter curated slug: %v", err)
	}
	if m.Alias != "claude-sonnet-4-6" || m.Upstream != "anthropic/claude-sonnet-4.6" {
		t.Errorf("openrouter curated slug = %+v", m)
	}
	// Any other vendor/model slug passes through untouched.
	m, err = ResolveClaudeSwitchModel("openrouter", "deepseek/deepseek-v4-pro")
	if err != nil {
		t.Fatalf("openrouter raw slug: %v", err)
	}
	if m.Alias != "deepseek/deepseek-v4-pro" || m.Upstream != "deepseek/deepseek-v4-pro" {
		t.Errorf("openrouter raw slug = %+v", m)
	}
	if _, err := ResolveClaudeSwitchModel("openrouter", "not-a-slug"); err == nil {
		t.Error("bare unknown openrouter model should be rejected")
	}
}

func TestClaudeTemplateStale(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		env      string
		want     bool
	}{
		{"zen curated alias", "opencode-zen", "ANTHROPIC_DEFAULT_SONNET_MODEL = \"claude-sonnet-5\"\n", false},
		{"deepseek 1M alias", "deepseek", "ANTHROPIC_DEFAULT_SONNET_MODEL = \"claude-opus-5[1m]\"\n", false},
		{"zen unknown alias", "opencode-zen", "ANTHROPIC_DEFAULT_SONNET_MODEL = \"my-model\"\n", true},
		{"openrouter raw slug", "openrouter", "ANTHROPIC_DEFAULT_SONNET_MODEL = \"anthropic/claude-sonnet-4.6\"\n", false},
		{"legacy tier alias", "deepseek", "ANTHROPIC_DEFAULT_HAIKU_MODEL = \"claude-haiku-4-5\"\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "tpl.toml")
			content := "model = \"sonnet\"\n\n[env]\nANTHROPIC_BASE_URL = \"http://127.0.0.1:2026\"\n" + tc.env
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			if got := claudeTemplateStale(path, tc.provider); got != tc.want {
				t.Errorf("claudeTemplateStale(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}
