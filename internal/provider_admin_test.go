package internal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestSetProviderModelMappingsAt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proxy.toml")
	content := `listen = "127.0.0.1:2026"
gateway_key = "aix-gateway"

[providers.deepseek-anthropic]
name = "DeepSeek-Anthropic"
upstream = "https://api.deepseek.com/anthropic"
auth_token = "sk-test"

  [providers.deepseek-anthropic.models]
  "claude-opus-5" = "deepseek-v4-flash"
  "claude-fable-5" = "deepseek-v4-pro"
`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	if err := SetProviderModelMappingsAt(path, DeepSeekAnthropicProviderID, map[string]string{
		ClaudeCodeDeepSeekModel: DeepSeekV4FlashModel,
	}); err != nil {
		t.Fatalf("SetProviderModelMappingsAt: %v", err)
	}

	var cfg ProxyConfig
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		t.Fatalf("reload proxy.toml: %v", err)
	}
	p := cfg.Providers[DeepSeekAnthropicProviderID]
	if p == nil {
		t.Fatal("deepseek-anthropic provider missing after write")
	}
	if got := p.Models[ClaudeCodeDeepSeekModel]; got != DeepSeekV4FlashModel {
		t.Errorf("%s mapping = %q, want %q", ClaudeCodeDeepSeekModel, got, DeepSeekV4FlashModel)
	}
	if got := p.Models[ClaudeCodeDeepSeekProModel]; got != DeepSeekV4ProModel {
		t.Errorf("%s mapping = %q, want existing value preserved", ClaudeCodeDeepSeekProModel, got)
	}
	if got := p.Upstream; got != "https://api.deepseek.com/anthropic" {
		t.Errorf("upstream = %q, want unchanged", got)
	}
}

func TestWriteProxyConfigAtUsesPrivatePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxy.toml")
	if err := WriteProxyConfigAt(path, DefaultProxyConfig()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("proxy config permissions = %o, want 600", got)
	}
}

func TestSetDeepSeekClaudeMappings(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ProxyConfigPath(), []byte(`listen = "127.0.0.1:2026"
gateway_key = "aix-gateway"

[providers.deepseek-anthropic]
name = "DeepSeek-Anthropic"
upstream = "https://api.deepseek.com/anthropic"
auth_token = "sk-test"

  [providers.deepseek-anthropic.models]
  "claude-sonnet-4-6" = "deepseek-v4-pro"
  "claude-haiku-4-5" = "deepseek-v4-flash"
`), 0600); err != nil {
		t.Fatal(err)
	}

	if err := SetDeepSeekClaudeMappings(); err != nil {
		t.Fatalf("SetDeepSeekClaudeMappings: %v", err)
	}

	cfg, err := LoadProxyConfig()
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Providers[DeepSeekAnthropicProviderID]
	if p == nil {
		t.Fatal("deepseek-anthropic provider missing")
	}
	if got := p.Models[ClaudeCodeDeepSeekModel]; got != DeepSeekV4FlashModel {
		t.Errorf("%s = %q, want %q", ClaudeCodeDeepSeekModel, got, DeepSeekV4FlashModel)
	}
	if got := p.Models[ClaudeCodeDeepSeekProModel]; got != DeepSeekV4ProModel {
		t.Errorf("%s = %q, want %q", ClaudeCodeDeepSeekProModel, got, DeepSeekV4ProModel)
	}
	harness, _ := HarnessProvider(HarnessClaude, "deepseek")
	if len(p.Models) != len(harness.Models)*2 {
		t.Errorf("models = %v, want standard and 1M mappings for %d harness models", p.Models, len(harness.Models))
	}
	for _, model := range harness.Models {
		if got := p.Models[model.ClientModel]; got != model.UpstreamModel {
			t.Errorf("%s = %q, want %q", model.ClientModel, got, model.UpstreamModel)
		}
		if got := p.Models[ClaudeDesktop1MModelID(model.ClientModel)]; got != model.UpstreamModel {
			t.Errorf("%s = %q, want %q", ClaudeDesktop1MModelID(model.ClientModel), got, model.UpstreamModel)
		}
	}
}

func TestSetDeepSeekClaudeMappingsAddsDynamicModel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ProxyConfigPath(), []byte(`[providers.deepseek-anthropic]
name = "DeepSeek-Anthropic"
upstream = "https://api.deepseek.com/anthropic"
auth_token = "sk-test"
`), 0600); err != nil {
		t.Fatal(err)
	}
	const model = "deepseek-v4-flash-vision-exp"
	if err := SetDeepSeekClaudeMappings(model); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadProxyConfig()
	if err != nil {
		t.Fatal(err)
	}
	alias, _ := ClaudeDeepSeekAlias(model)
	if got := cfg.Providers[DeepSeekAnthropicProviderID].Models[alias]; got != model {
		t.Errorf("dynamic mapping = %q, want %q", got, model)
	}
	if got := cfg.Providers[DeepSeekAnthropicProviderID].Models[ClaudeDesktop1MModelID(alias)]; got != model {
		t.Errorf("dynamic 1M mapping = %q, want %q", got, model)
	}
}

func TestSetProviderModelMappingsAtMissingProvider(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proxy.toml")
	if err := os.WriteFile(path, []byte("listen = \"127.0.0.1:2026\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := SetProviderModelMappingsAt(path, DeepSeekAnthropicProviderID, map[string]string{
		ClaudeCodeDeepSeekModel: DeepSeekV4FlashModel,
	}); err == nil {
		t.Fatal("expected an error for a missing provider")
	}
}

func TestLoadProxyConfigForWriteMigratesLegacyGatewayKeys(t *testing.T) {
	for _, legacy := range []string{"aix-gateway", "ats-gateway"} {
		t.Run(legacy, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			if err := EnsureDirs(); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(ProxyConfigPath(), []byte("gateway_key = \""+legacy+"\"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadProxyConfigForWrite()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.GatewayKey != DefaultGatewayAPIKey {
				t.Fatalf("gateway key = %q, want %q", cfg.GatewayKey, DefaultGatewayAPIKey)
			}
		})
	}
}

func TestLoadProxyConfigForWritePreservesCustomGatewayKey(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ProxyConfigPath(), []byte("gateway_key = \"my-private-key\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadProxyConfigForWrite()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GatewayKey != "my-private-key" {
		t.Fatalf("custom gateway key changed to %q", cfg.GatewayKey)
	}
}

func TestEnsureClaudeProxyProviderCreatesSection(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENROUTER_API_KEY", "sk-or-test")
	if err := EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := EnsureClaudeProxyProvider("openrouter"); err != nil {
		t.Fatalf("EnsureClaudeProxyProvider: %v", err)
	}
	cfg, err := LoadProxyConfig()
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Providers["openrouter"]
	if p == nil {
		t.Fatal("openrouter provider missing after ensure")
	}
	if p.Upstream != "https://openrouter.ai/api" {
		t.Errorf("upstream = %q, want https://openrouter.ai/api", p.Upstream)
	}
	if !p.AnthropicNative {
		t.Error("anthropic flag must be set")
	}
	if p.AuthToken != "sk-or-test" {
		t.Errorf("auth_token = %q, want sk-or-test", p.AuthToken)
	}
	if got := p.Models["claude-opus-5"]; got != "anthropic/claude-opus-5" {
		t.Errorf("claude-opus-5 mapping = %q, want anthropic/claude-opus-5", got)
	}
	// Existing sections must be preserved (user edits win).
	cfg.Providers["openrouter"].AuthToken = "sk-user-edited"
	if err := WriteProxyConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if err := EnsureClaudeProxyProvider("openrouter"); err != nil {
		t.Fatal(err)
	}
	reloaded, _ := LoadProxyConfig()
	if got := reloaded.Providers["openrouter"].AuthToken; got != "sk-user-edited" {
		t.Errorf("Ensure must not clobber an existing section, auth_token = %q", got)
	}
}

func TestEnsureClaudeProxyProviderFallsBackToSharedKeys(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENCODE_GO_API_KEY", "")
	t.Setenv("OPENCODE_ZEN_API_KEY", "")
	t.Setenv("OPENCODE_API_KEY", "sk-shared")
	if err := EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := EnsureClaudeProxyProvider("opencode-go"); err != nil {
		t.Fatalf("EnsureClaudeProxyProvider: %v", err)
	}
	cfg, _ := LoadProxyConfig()
	if got := cfg.Providers["opencode-go"].AuthToken; got != "sk-shared" {
		t.Errorf("opencode-go auth_token = %q, want sk-shared", got)
	}
}

func TestEnsureClaudeProxyProviderRequiresKey(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENROUTER_API_KEY", "")
	if err := EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := EnsureClaudeProxyProvider("openrouter"); err == nil {
		t.Fatal("expected an error when no API key is available")
	}
}
