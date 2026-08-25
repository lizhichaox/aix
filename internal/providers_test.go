package internal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexTemplateStale(t *testing.T) {
	preset := ProviderPreset{
		Name:       "Kimi",
		CodexModel: "kimi-k2.7-code",
		Models:     map[string]string{"kimi-k2.7-code": "kimi-k2.7-code"},
	}

	dir := t.TempDir()

	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name:    "current preset model",
			content: "mode = \"proxy\"\nmodel = \"kimi-k2.7-code\"\nmodel_provider = \"kimi\"\n",
			want:    false,
		},
		{
			name:    "valid source model key",
			content: "model = \"kimi-k2.7-code\"\nmodel_provider = \"kimi\"\n",
			want:    false,
		},
		{
			name:    "valid upstream model value",
			content: "model = \"kimi-k2.7-code\"\nmodel_provider = \"kimi\"\n",
			want:    false,
		},
		{
			name:    "stale old preset model",
			content: "model = \"kimi-k2\"\nmodel_provider = \"kimi\"\n",
			want:    true,
		},
		{
			name:    "empty model",
			content: "model_provider = \"kimi\"\n",
			want:    true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name+".toml")
			if err := os.WriteFile(path, []byte(tc.content), 0600); err != nil {
				t.Fatal(err)
			}
			if got := codexTemplateStale(path, "kimi", preset); got != tc.want {
				t.Errorf("codexTemplateStale() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCodexDeepSeekLegacyTemplateIsStale(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deepseek.toml")
	content := `description = "DeepSeek via aix proxy"
model = "deepseek-v4-pro"
model_provider = "deepseek"

[model_providers.deepseek]
name = "DeepSeek"
base_url = "http://127.0.0.1:2026/deepseek/v1"
wire_api = "responses"
experimental_bearer_token = "aix-gateway"
`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if !codexTemplateStale(path, "deepseek", KnownProviders()["deepseek"]) {
		t.Error("legacy proxy template without mode should be stale")
	}
	// Explicit proxy mode is current: the gateway passes Responses through
	// without protocol conversion.
	optOut := "description = \"DeepSeek via aix proxy\"\nmode = \"proxy\"\nmodel = \"deepseek-v4-pro\"\n"
	if err := os.WriteFile(path, []byte(optOut), 0600); err != nil {
		t.Fatal(err)
	}
	if codexTemplateStale(path, "deepseek", KnownProviders()["deepseek"]) {
		t.Error("explicit mode = \"proxy\" template should be current")
	}
	native := "mode = \"native\"\nmodel = \"deepseek-v4-pro\"\n"
	if err := os.WriteFile(path, []byte(native), 0600); err != nil {
		t.Fatal(err)
	}
	if !codexTemplateStale(path, "deepseek", KnownProviders()["deepseek"]) {
		t.Error("explicit mode = \"native\" template should be stale")
	}
}

func TestCodexTemplateStaleAnyModelProvider(t *testing.T) {
	preset := ProviderPreset{CodexModel: "openai/gpt-5.3-codex", CodexAnyModel: true}
	dir := t.TempDir()
	path := filepath.Join(dir, "openrouter.toml")

	// A user-chosen arbitrary model slug must be preserved, not regenerated.
	if err := os.WriteFile(path, []byte("mode = \"proxy\"\nmodel = \"my/vendor-model\"\nmodel_provider = \"openrouter\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if codexTemplateStale(path, "openrouter", preset) {
		t.Error("arbitrary model should not be stale for an any-model provider")
	}

	// An empty model still needs regeneration.
	if err := os.WriteFile(path, []byte("model_provider = \"openrouter\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if !codexTemplateStale(path, "openrouter", preset) {
		t.Error("empty model should be stale")
	}
}

func TestEnsureProviderTemplateCreatesAndKeepsEdits(t *testing.T) {
	// EnsureProviderTemplate writes into the real ~/.aix layout, so only
	// verify the known-provider behavior indirectly through content helpers.
	content := ProviderTemplateContent("codex", "deepseek", KnownProviders()["deepseek"])
	if content == "" || !strings.Contains(content, "mode = \"proxy\"") {
		t.Errorf("codex/deepseek template should use the gateway: %q", content)
	}
	claude := ProviderTemplateContent("claudecode", "deepseek", KnownProviders()["deepseek"])
	defaultClaude, _ := ClaudeModelFor("deepseek", DefaultClaudeUpstreamModel)
	if !strings.Contains(claude, "ANTHROPIC_DEFAULT_SONNET_MODEL = \""+ClaudeCodeModelID(defaultClaude)+"\"") {
		t.Errorf("claudecode/deepseek template should default to 1M: %q", claude)
	}
	if !strings.Contains(claude, "CLAUDE_CODE_EFFORT_LEVEL = \"high\"") {
		t.Errorf("claudecode/deepseek template should default to high effort: %q", claude)
	}
	desktop := ProviderTemplateContent("desktop", "deepseek", KnownProviders()["deepseek"])
	if strings.Contains(desktop, "gateway_key") {
		t.Errorf("desktop template must not cache the proxy gateway key: %q", desktop)
	}
	kimi := ProviderTemplateContent("codex", "kimi", KnownProviders()["kimi"])
	if kimi != "" {
		t.Errorf("codex/kimi template should not be generated (proxy providers removed): %q", kimi)
	}
	if created, err := EnsureProviderTemplate("codex", "pro"); err != nil || created {
		t.Errorf("unknown provider should not be auto-generated: created=%v err=%v", created, err)
	}
}

func TestCustomProviderTemplateContent(t *testing.T) {
	codex := CustomProviderTemplateContent("codex", "myprovider", "My Provider", "my-model", "127.0.0.1:2026")
	if codex != "" {
		t.Errorf("codex should not get a custom proxy template: %q", codex)
	}
	excalidraw := CustomProviderTemplateContent("excalidraw", "myprovider", "My Provider", "my-model", "127.0.0.1:2026")
	if !strings.Contains(excalidraw, "model = \"my-model\"") {
		t.Errorf("excalidraw custom template mismatch: %q", excalidraw)
	}
	if got := CustomProviderTemplateContent("claudecode", "myprovider", "My Provider", "my-model", "127.0.0.1:2026"); got != "" {
		t.Errorf("claudecode should not get a custom template: %q", got)
	}
}

func TestProviderTemplateContentNativeUsesSpec(t *testing.T) {
	content := ProviderTemplateContent("codex", "deepseek", KnownProviders()["deepseek"])
	if !strings.Contains(content, "DeepSeek via AIX gateway (Responses passthrough)") || !strings.Contains(content, "model = \""+DeepSeekV4VisionModel+"\"") {
		t.Errorf("managed template should use spec name/default model: %q", content)
	}
}
