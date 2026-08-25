package internal

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

func migrateLegacyGatewayKey(key string) string {
	switch strings.TrimSpace(key) {
	case "", "aix-gateway", "ats-gateway":
		return DefaultGatewayAPIKey
	default:
		return key
	}
}

// LoadProxyConfigForWrite loads proxy.toml for a write operation, returning a
// config with a non-nil Providers map when the file does not exist yet.
func LoadProxyConfigForWrite() (*ProxyConfig, error) {
	path := ProxyConfigPath()
	if _, statErr := os.Stat(path); statErr != nil && os.IsNotExist(statErr) {
		cfg := DefaultProxyConfig()
		cfg.Providers = map[string]*ProviderConfig{}
		return cfg, nil
	}
	cfg, err := LoadProxyConfig()
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", path, err)
	}
	if cfg.Providers == nil {
		cfg.Providers = map[string]*ProviderConfig{}
	}
	cfg.GatewayKey = migrateLegacyGatewayKey(cfg.GatewayKey)
	return cfg, nil
}

// WriteProxyConfig rewrites proxy.toml from cfg.
func WriteProxyConfig(cfg *ProxyConfig) error {
	return WriteProxyConfigAt(ProxyConfigPath(), cfg)
}

// WriteProxyConfigAt rewrites proxy.toml at an explicit path (used by tests).
func WriteProxyConfigAt(path string, cfg *ProxyConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Chmod(0600); err != nil {
		return err
	}
	if err := toml.NewEncoder(f).Encode(cfg); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// SetProviderModelMappings updates one or more source -> upstream model
// mappings for a proxy provider, preserving all other entries. It returns an
// error when the provider is not configured.
func SetProviderModelMappings(providerID string, mappings map[string]string) error {
	return SetProviderModelMappingsAt(ProxyConfigPath(), providerID, mappings)
}

// SetProviderModelMappingsAt is SetProviderModelMappings with an explicit
// proxy.toml path.
func SetProviderModelMappingsAt(path, providerID string, mappings map[string]string) error {
	if strings.TrimSpace(providerID) == "" {
		return fmt.Errorf("provider ID is required")
	}
	if len(mappings) == 0 {
		return fmt.Errorf("at least one model mapping is required")
	}
	cfg := DefaultProxyConfig()
	if _, err := toml.DecodeFile(path, cfg); err != nil {
		return fmt.Errorf("load %s: %w", path, err)
	}
	if cfg.Providers == nil {
		cfg.Providers = map[string]*ProviderConfig{}
	}
	p := cfg.Providers[providerID]
	if p == nil {
		return fmt.Errorf("provider %q not found in %s", providerID, path)
	}
	if p.Models == nil {
		p.Models = map[string]string{}
	}
	for src, dst := range mappings {
		if strings.TrimSpace(src) == "" || strings.TrimSpace(dst) == "" {
			return fmt.Errorf("source and upstream model names are required")
		}
		p.Models[src] = dst
	}
	return WriteProxyConfigAt(path, cfg)
}

// SetDeepSeekClaudeMappings writes the canonical Claude-side model mappings for
// the DeepSeek Anthropic provider: claude-opus-5 always routes to
// deepseek-v4-flash and claude-fable-5 to deepseek-v4-pro. When selectedModel
// is supplied, a stable dynamic Claude-shaped alias is also mapped to that
// upstream model so newly released DeepSeek models work without an AIX update.
func SetDeepSeekClaudeMappings(selectedModel ...string) error {
	cfg, err := LoadProxyConfigForWrite()
	if err != nil {
		return err
	}
	p := cfg.Providers[DeepSeekAnthropicProviderID]
	if p == nil {
		return fmt.Errorf("provider %q not found in %s", DeepSeekAnthropicProviderID, ProxyConfigPath())
	}
	harness, ok := HarnessProvider(HarnessClaude, "deepseek")
	if !ok {
		return fmt.Errorf("deepseek has no Claude harness mapping in %s", HarnessRegistryPath())
	}
	p.Models = make(map[string]string, len(harness.Models)*2+2)
	for _, model := range harness.Models {
		p.Models[model.ClientModel] = model.UpstreamModel
		if model.ContextWindow >= oneMillionContext {
			p.Models[ClaudeDesktop1MModelID(model.ClientModel)] = model.UpstreamModel
		}
	}
	if len(selectedModel) > 0 && selectedModel[0] != "" {
		alias, _ := ClaudeDeepSeekAlias(selectedModel[0])
		if alias == "" {
			return fmt.Errorf("invalid DeepSeek model id %q", selectedModel[0])
		}
		p.Models[alias] = selectedModel[0]
		p.Models[ClaudeDesktop1MModelID(alias)] = selectedModel[0]
	}
	return WriteProxyConfig(cfg)
}

// claudeProviderEnvKey returns the API key for an Anthropic-native preset from
// the environment, falling back across shared aliases where applicable.
func claudeProviderEnvKey(providerID string) string {
	switch providerID {
	case "deepseek":
		return os.Getenv("DEEPSEEK_API_KEY")
	case "opencode-zen":
		if k := os.Getenv("OPENCODE_ZEN_API_KEY"); k != "" {
			return k
		}
		return os.Getenv("OPENCODE_API_KEY")
	case "opencode-go":
		if k := os.Getenv("OPENCODE_GO_API_KEY"); k != "" {
			return k
		}
		if k := os.Getenv("OPENCODE_ZEN_API_KEY"); k != "" {
			return k
		}
		return os.Getenv("OPENCODE_API_KEY")
	case "openrouter":
		return os.Getenv("OPENROUTER_API_KEY")
	}
	return ""
}

// EnsureClaudeProxyProvider ensures proxy.toml carries the Anthropic-native
// provider section for a Claude preset. Existing sections are left untouched
// (user edits are preserved); a missing section is created with the curated
// Claude model mappings and an auth token from the environment.
func EnsureClaudeProxyProvider(providerID string) error {
	preset, ok := KnownProviders()[providerID]
	if !ok || !preset.AnthropicNative {
		return fmt.Errorf("provider %q is not an Anthropic-native preset", providerID)
	}
	proxyID := ClaudeProxyProviderID(providerID)
	harness, ok := HarnessProvider(HarnessClaude, providerID)
	if !ok {
		return fmt.Errorf("provider %q has no Claude harness mapping in %s", providerID, HarnessRegistryPath())
	}
	cfg, err := LoadProxyConfigForWrite()
	if err != nil {
		return err
	}
	p := cfg.Providers[proxyID]
	key := claudeProviderEnvKey(providerID)
	if p != nil && p.AuthToken != "" {
		key = p.AuthToken
	}
	if key == "" {
		return fmt.Errorf("provider %q needs $%s (or add auth_token to %s)", providerID, preset.EnvVar, ProxyConfigPath())
	}
	models := make(map[string]string, len(harness.Models)*2)
	for _, model := range harness.Models {
		models[model.ClientModel] = model.UpstreamModel
		if model.ContextWindow >= oneMillionContext {
			models[ClaudeDesktop1MModelID(model.ClientModel)] = model.UpstreamModel
		}
	}
	cfg.Providers[proxyID] = &ProviderConfig{
		Name:            preset.Name + "-Anthropic",
		Upstream:        harness.BaseURL,
		AuthToken:       key,
		Models:          models,
		AnthropicNative: true,
	}
	return WriteProxyConfig(cfg)
}

// CodexProxyProviderID returns the private proxy.toml provider ID used for a
// Codex Responses route. The prefix keeps Codex routing and model mappings
// independent from the Anthropic-shaped route used by Claude.
func CodexProxyProviderID(providerID string) string {
	return "codex-" + providerID
}

// EnsureCodexProxyProvider configures a native Responses passthrough route and
// returns the local base URL and gateway credential that Codex should use.
func EnsureCodexProxyProvider(providerID, upstreamKey string) (string, string, error) {
	spec, ok := NativeProvider(providerID)
	if !ok {
		return "", "", fmt.Errorf("provider %q is not a Codex Responses provider", providerID)
	}
	harness, ok := HarnessProvider(HarnessCodex, providerID)
	if !ok {
		return "", "", fmt.Errorf("provider %q has no Codex harness mapping in %s", providerID, HarnessRegistryPath())
	}
	cfg, err := LoadProxyConfigForWrite()
	if err != nil {
		return "", "", err
	}
	proxyID := CodexProxyProviderID(providerID)
	key := strings.TrimSpace(upstreamKey)
	if existing := cfg.Providers[proxyID]; existing != nil && strings.TrimSpace(existing.AuthToken) != "" {
		key = strings.TrimSpace(existing.AuthToken)
	}
	if key == "" {
		return "", "", fmt.Errorf("provider %q needs $%s (or add auth_token to %s)", providerID, spec.EnvKey, ProxyConfigPath())
	}
	models := make(map[string]string, len(harness.Models))
	for _, model := range harness.Models {
		models[model.ClientModel] = model.UpstreamModel
	}
	cfg.Providers[proxyID] = &ProviderConfig{
		Name:      spec.Name + "-Responses",
		Upstream:  codexProxyUpstream(harness.BaseURL),
		AuthToken: key,
		Models:    models,
	}
	if err := WriteProxyConfig(cfg); err != nil {
		return "", "", err
	}
	listen := cfg.Listen
	if strings.HasPrefix(listen, "0.0.0.0:") {
		listen = "127.0.0.1:" + strings.TrimPrefix(listen, "0.0.0.0:")
	}
	return "http://" + listen + "/" + proxyID + "/v1", cfg.GatewayKey, nil
}

func codexProxyUpstream(baseURL string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	return strings.TrimSuffix(baseURL, "/v1")
}
