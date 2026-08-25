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
