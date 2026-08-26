package internal

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
)

func TestDeepSeekAPIKeyPrefersEnv(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "sk-env-key")
	key, source := DeepSeekAPIKey()
	if key != "sk-env-key" || source != "$DEEPSEEK_API_KEY" {
		t.Fatalf("DeepSeekAPIKey() = (%q, %q), want env key", key, source)
	}
}

func TestResolveCodexDeepSeekModel(t *testing.T) {
	if got, err := ResolveCodexDeepSeekModel(""); err != nil || got != DeepSeekV4VisionModel {
		t.Fatalf("default model = (%q, %v), want %q", got, err, DeepSeekV4VisionModel)
	}
	if got, err := ResolveCodexDeepSeekModel(DeepSeekV4FlashModel); err != nil || got != DeepSeekV4FlashModel {
		t.Fatalf("flash model = (%q, %v), want %q", got, err, DeepSeekV4FlashModel)
	}
	if _, err := ResolveCodexDeepSeekModel("gpt-5.5"); err == nil {
		t.Fatal("ResolveCodexDeepSeekModel should reject unsupported models")
	}
}

func TestNativeProviderRegistry(t *testing.T) {
	spec, ok := NativeProvider("deepseek")
	if !ok {
		t.Fatal("deepseek should be a registered native provider")
	}
	if spec.EnvKey != "DEEPSEEK_API_KEY" || spec.BaseURL == "" || spec.DefaultModel != DeepSeekV4VisionModel {
		t.Errorf("unexpected deepseek spec: %+v", spec)
	}
	if got := NativeModels("deepseek"); len(got) != 3 || got[0] != DeepSeekV4FlashModel || got[1] != DeepSeekV4ProModel || got[2] != DeepSeekV4VisionModel {
		t.Errorf("NativeModels(deepseek) = %v", got)
	}
	if IsNativeProvider("kimi") || IsNativeProvider("qwen") {
		t.Error("kimi/qwen must not be native providers until registered")
	}
	if _, ok := NativeProvider("nope"); ok {
		t.Error("unknown provider should not resolve")
	}
}

func TestNewNativeProvidersRegistered(t *testing.T) {
	want := map[string]struct {
		name       string
		envKey     string
		baseURL    string
		defaultMdl string
		anyModel   bool
	}{
		"opencode-zen": {
			name:       "OpenCode Zen",
			envKey:     "OPENCODE_ZEN_API_KEY",
			baseURL:    "https://opencode.ai/zen/v1",
			defaultMdl: "deepseek-v4-flash",
		},
		"opencode-go": {
			name:       "OpenCode Go",
			envKey:     "OPENCODE_GO_API_KEY",
			baseURL:    "https://opencode.ai/zen/go/v1",
			defaultMdl: "deepseek-v4-flash-vision-exp",
			anyModel:   true,
		},
		"openrouter": {
			name:       "OpenRouter",
			envKey:     "OPENROUTER_API_KEY",
			baseURL:    "https://openrouter.ai/api/v1",
			defaultMdl: "deepseek/deepseek-v4-flash-vision-exp",
			anyModel:   true,
		},
	}
	for id, wantSpec := range want {
		spec, ok := NativeProvider(id)
		if !ok {
			t.Fatalf("%s should be a registered native provider", id)
		}
		if spec.Name != wantSpec.name || spec.EnvKey != wantSpec.envKey ||
			spec.BaseURL != wantSpec.baseURL || spec.DefaultModel != wantSpec.defaultMdl ||
			spec.AllowAnyModel != wantSpec.anyModel {
			t.Errorf("%s spec = %+v, want %+v", id, spec, wantSpec)
		}
		if spec.DefaultModel == "" {
			t.Errorf("%s must have a default model", id)
		}
		if len(spec.Models) == 0 {
			t.Errorf("%s must list at least one model", id)
		}
	}
	// OpenCode Go accepts any model on its Responses endpoint (the gateway
	// translates to chat/completions), so the curated list is only a starting
	// point and the default model must be present in it.
	if models := NativeModels("opencode-go"); len(models) < 2 || models[0] != "gpt-5.6-luna" {
		t.Errorf("NativeModels(opencode-go) = %v, want a curated list starting with gpt-5.6-luna", models)
	}
}

func TestNativeProviderAPIKeyPrefersEnvThenAliases(t *testing.T) {
	// Isolate from any keys already present in the developer's environment.
	t.Setenv("OPENCODE_ZEN_API_KEY", "")
	t.Setenv("OPENCODE_GO_API_KEY", "")
	t.Setenv("OPENCODE_API_KEY", "sk-shared")
	if key, source := NativeProviderAPIKey("opencode-zen"); key != "sk-shared" || source != "$OPENCODE_API_KEY" {
		t.Fatalf("opencode-zen via shared alias = (%q, %q)", key, source)
	}
	t.Setenv("OPENCODE_ZEN_API_KEY", "sk-zen")
	t.Setenv("OPENCODE_GO_API_KEY", "sk-go")
	if key, source := NativeProviderAPIKey("opencode-go"); key != "sk-go" || source != "$OPENCODE_GO_API_KEY" {
		t.Fatalf("opencode-go should prefer its own env var over aliases: (%q, %q)", key, source)
	}
	t.Setenv("OPENCODE_GO_API_KEY", "")
	if key, source := NativeProviderAPIKey("opencode-go"); key != "sk-zen" || source != "$OPENCODE_ZEN_API_KEY" {
		t.Fatalf("opencode-go should fall back to the zen alias: (%q, %q)", key, source)
	}
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("OPENROUTER_API_KEY", "sk-or")
	if key, source := NativeProviderAPIKey("openrouter"); key != "sk-or" || source != "$OPENROUTER_API_KEY" {
		t.Fatalf("openrouter = (%q, %q)", key, source)
	}
}

func TestNativeProviderAPIKeyUsesSharedClaudeCredential(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("DEEPSEEK_API_KEY", "")
	if err := EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultProxyConfig()
	cfg.Providers = make(map[string]*ProviderConfig)
	cfg.Providers[DeepSeekAnthropicProviderID] = &ProviderConfig{AuthToken: "sk-shared-deepseek"}
	if err := WriteProxyConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if key, source := NativeProviderAPIKey("deepseek"); key != "sk-shared-deepseek" || source != "AIX provider configuration" {
		t.Fatalf("deepseek shared credential = (%q, %q)", key, source)
	}
}

func TestResolveNativeModelNewProviders(t *testing.T) {
	if got, err := ResolveNativeModel("opencode-zen", ""); err != nil || got != "deepseek-v4-flash" {
		t.Fatalf("opencode-zen default = (%q, %v)", got, err)
	}
	if got, err := ResolveNativeModel("opencode-zen", "gpt-5.6-sol"); err != nil || got != "gpt-5.6-sol" {
		t.Fatalf("opencode-zen known model = (%q, %v)", got, err)
	}
	if _, err := ResolveNativeModel("opencode-zen", "some/random-model"); err == nil {
		t.Error("opencode-zen should reject models outside its Responses catalog")
	}
	if got, err := ResolveNativeModel("opencode-go", ""); err != nil || got != "deepseek-v4-flash-vision-exp" {
		t.Fatalf("opencode-go default = (%q, %v)", got, err)
	}
	if got, err := ResolveNativeModel("opencode-go", "deepseek-v4-flash"); err != nil || got != "deepseek-v4-flash" {
		t.Fatalf("opencode-go known model = (%q, %v)", got, err)
	}
	// OpenRouter accepts any vendor/model slug.
	if got, err := ResolveNativeModel("openrouter", "some/vendor-model"); err != nil || got != "some/vendor-model" {
		t.Fatalf("openrouter arbitrary model = (%q, %v)", got, err)
	}
	if got, err := ResolveNativeModel("openrouter", ""); err != nil || got != "deepseek/deepseek-v4-flash-vision-exp" {
		t.Fatalf("openrouter default = (%q, %v)", got, err)
	}
}

func TestResolveNativeModelHumanReadableNames(t *testing.T) {
	cases := []struct {
		provider string
		model    string
		want     string
	}{
		{"opencode-go", "DeepSeek V4 Flash", "deepseek-v4-flash"},
		{"opencode-go", "DeepSeek V4 Flash Vision Exp", "deepseek-v4-flash-vision-exp"},
		{"opencode-go", "GLM 5.2", "glm-5.2"},
		{"openrouter", "DeepSeek V4 Pro", "deepseek/deepseek-v4-pro"},
		{"openrouter", "deepseek/deepseek-v4-flash", "deepseek/deepseek-v4-flash"},
		{"deepseek", "DeepSeek V4 Flash", "deepseek-v4-flash"},
	}
	for _, tc := range cases {
		got, err := ResolveNativeModel(tc.provider, tc.model)
		if err != nil {
			t.Errorf("ResolveNativeModel(%q, %q): %v", tc.provider, tc.model, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ResolveNativeModel(%q, %q) = %q, want %q", tc.provider, tc.model, got, tc.want)
		}
	}
}

func TestResolveNativeModelOpenRouterNormalizesSlugs(t *testing.T) {
	for in, want := range map[string]string{
		"deepseek-v4-flash-latest":          "~deepseek/deepseek-v4-flash-latest",
		"deepseek/deepseek-v4-flash-latest": "~deepseek/deepseek-v4-flash-latest",
		"deepseek-v4-flash":                 "deepseek/deepseek-v4-flash",
		"deepseek-v4-pro":                   "deepseek/deepseek-v4-pro",
	} {
		if got, err := ResolveNativeModel("openrouter", in); err != nil || got != want {
			t.Errorf("ResolveNativeModel(openrouter, %q) = (%q, %v), want %q", in, got, err, want)
		}
	}
	// OpenAI and Anthropic slugs are no longer curated for OpenRouter; the bare
	// aliases were removed so those names are rejected unless vendor-prefixed.
	for _, rejected := range []string{"not-a-model", "gpt-5.3-codex", "claude-sonnet-4.6"} {
		if _, err := ResolveNativeModel("openrouter", rejected); err == nil {
			t.Errorf("bare %q should be rejected for openrouter", rejected)
		}
	}
	if got, err := ResolveNativeModel("openrouter", "some/vendor-model"); err != nil || got != "some/vendor-model" {
		t.Fatalf("vendor-prefixed openrouter model = (%q, %v)", got, err)
	}
}

func TestNewProviderCatalogEntriesCarryRequiredFields(t *testing.T) {
	for _, id := range []string{"opencode-zen", "opencode-go", "openrouter"} {
		spec, ok := NativeProvider(id)
		if !ok {
			t.Fatalf("%s not registered", id)
		}
		entry, err := catalogEntryFromSpec(spec, spec.DefaultModel)
		if err != nil {
			t.Fatalf("catalogEntryFromSpec(%s): %v", id, err)
		}
		if instructions, _ := entry["base_instructions"].(string); instructions == "" {
			t.Errorf("%s catalog entry missing base_instructions (required by Codex 0.147+)", id)
		}
		if entry["context_window"] == nil {
			t.Errorf("%s catalog entry missing context_window", id)
		}
	}
}

func TestDeepSeekCatalogEntriesCarryOneMillionContext(t *testing.T) {
	deepseekModels := []struct {
		provider string
		model    string
	}{
		{"opencode-go", "deepseek-v4-flash"},
		{"opencode-go", "deepseek-v4-pro"},
		{"openrouter", "deepseek/deepseek-v4-flash"},
		{"openrouter", "deepseek/deepseek-v4-pro"},
		{"openrouter", "~deepseek/deepseek-v4-flash-latest"},
		{"openrouter", "deepseek/deepseek-v4-flash-vision-exp"},
	}
	for _, tt := range deepseekModels {
		spec, ok := NativeProvider(tt.provider)
		if !ok {
			t.Fatalf("%s not registered", tt.provider)
		}
		entry, err := catalogEntryFromSpec(spec, tt.model)
		if err != nil {
			t.Fatalf("catalogEntryFromSpec(%s, %s): %v", tt.provider, tt.model, err)
		}
		if got, _ := entry["context_window"].(float64); got != 1048576 {
			t.Errorf("%s %s context_window = %v, want 1048576", tt.provider, tt.model, got)
		}
		if got, _ := entry["max_context_window"].(float64); got != 1048576 {
			t.Errorf("%s %s max_context_window = %v, want 1048576", tt.provider, tt.model, got)
		}
	}

	// Non-DeepSeek models on the same providers keep the conservative
	// provider-level fallback until their upstream window is verified.
	checks := []struct {
		provider string
		model    string
	}{
		// OpenCode Go's default is non-DeepSeek; OpenRouter's curated list is
		// all-DeepSeek now, so use an explicit non-DeepSeek slug (AllowAnyModel)
		// to exercise the provider-level fallback.
		{"opencode-go", "gpt-5.6-luna"},
		{"openrouter", "some/vendor-model"},
	}
	for _, c := range checks {
		spec, ok := NativeProvider(c.provider)
		if !ok {
			t.Fatalf("%s not registered", c.provider)
		}
		entry, err := catalogEntryFromSpec(spec, c.model)
		if err != nil {
			t.Fatalf("catalogEntryFromSpec(%s, %s): %v", c.provider, c.model, err)
		}
		if got, _ := entry["context_window"].(float64); got != 400000 {
			t.Errorf("%s %s context_window = %v, want provider fallback 400000", c.provider, c.model, got)
		}
	}
}

func TestConfigureOpenRouterWritesArbitraryActiveModel(t *testing.T) {
	dir := t.TempDir()
	opts := CodexNativeOptions{
		ProviderID:       "openrouter",
		APIKey:           "sk-or",
		Model:            "some/vendor-model",
		Effort:           "max",
		ConfigPath:       filepath.Join(dir, "config.toml"),
		ModelCatalogPath: filepath.Join(dir, "models.json"),
		BackupDir:        filepath.Join(dir, "backups"),
	}
	if err := ConfigureCodexNativeAt(opts); err != nil {
		t.Fatalf("ConfigureCodexNativeAt: %v", err)
	}
	config, err := readTomlMap(opts.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if config["model"] != "some/vendor-model" {
		t.Errorf("config model = %v, want some/vendor-model", config["model"])
	}
	if config["model_reasoning_effort"] != "max" {
		t.Errorf("config effort = %v, want max", config["model_reasoning_effort"])
	}
	var catalog struct {
		Models []struct {
			Slug string `json:"slug"`
		} `json:"models"`
	}
	raw, err := os.ReadFile(opts.ModelCatalogPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		t.Fatal(err)
	}
	slugs := make(map[string]bool)
	for _, m := range catalog.Models {
		slugs[m.Slug] = true
	}
	for _, want := range []string{"deepseek/deepseek-v4-pro", "some/vendor-model"} {
		if !slugs[want] {
			t.Errorf("catalog missing %q (got %v)", want, slugs)
		}
	}
}

func TestConfigureCodexUsesEditableHarnessBaseURL(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	registry := BundledHarnessRegistry()
	provider := registry.Providers["deepseek"]
	codex := provider.Harnesses[HarnessCodex]
	codex.BaseURL = "https://responses.example.test/v1"
	codex.DefaultEffort = "max"
	defaultModel := codex.Models[codex.DefaultModel]
	defaultModel.SupportedEfforts = []string{"high", "max"}
	defaultModel.DefaultEffort = "max"
	codex.Models[codex.DefaultModel] = defaultModel
	provider.Harnesses[HarnessCodex] = codex
	registry.Providers["deepseek"] = provider
	if err := WriteHarnessRegistry(HarnessRegistryPath(), registry); err != nil {
		t.Fatal(err)
	}
	opts := CodexNativeOptions{
		ProviderID:       "deepseek",
		APIKey:           "sk-test",
		ConfigPath:       filepath.Join(home, ".codex", "config.toml"),
		ModelCatalogPath: filepath.Join(home, ".codex", "models.json"),
		BackupDir:        filepath.Join(home, ".aix", "backups"),
	}
	if err := ConfigureCodexNativeAt(opts); err != nil {
		t.Fatal(err)
	}
	config, err := readTomlMap(opts.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	providers, _ := config["model_providers"].(map[string]interface{})
	deepseek, _ := providers["deepseek"].(map[string]interface{})
	if got := deepseek["base_url"]; got != codex.BaseURL {
		t.Errorf("base_url = %v, want %s", got, codex.BaseURL)
	}
	if got := config["model_reasoning_effort"]; got != "max" {
		t.Errorf("model_reasoning_effort = %v, want max", got)
	}
	var catalog struct {
		Models []map[string]interface{} `json:"models"`
	}
	raw, err := os.ReadFile(opts.ModelCatalogPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range catalog.Models {
		if entry["slug"] == codex.DefaultModel {
			found = true
			if entry["default_reasoning_level"] != "max" {
				t.Errorf("catalog default effort = %v", entry["default_reasoning_level"])
			}
			levels, _ := entry["supported_reasoning_levels"].([]interface{})
			if len(levels) != 2 {
				t.Errorf("catalog effort levels = %v", levels)
			}
		}
	}
	if !found {
		t.Errorf("catalog missing default model %q", codex.DefaultModel)
	}
}

func TestApplyCodexNativeProviderEndToEnd(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENCODE_ZEN_API_KEY", "sk-zen")

	app, err := ResolveApp("codex")
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyProviderWithModel(app, "opencode-zen", ""); err != nil {
		t.Fatalf("ApplyProviderWithModel: %v", err)
	}

	// The per-app template was auto-created in managed passthrough mode.
	if _, err := os.Stat(ProviderPath("codex", "opencode-zen")); err != nil {
		t.Fatalf("template was not created: %v", err)
	}
	raw, err := os.ReadFile(ProviderPath("codex", "opencode-zen"))
	if err != nil {
		t.Fatal(err)
	}
	if content := string(raw); !strings.Contains(content, "mode = \"proxy\"") ||
		!strings.Contains(content, "model = \"deepseek-v4-flash\"") {
		t.Errorf("unexpected native template: %s", content)
	}

	// Codex receives the local route and gateway key; the upstream credential
	// stays in AIX's private provider configuration.
	config, err := readTomlMap(CodexConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if config["model_provider"] != "opencode-zen" || config["model"] != "deepseek-v4-flash" {
		t.Errorf("config = %#v", config)
	}
	providers, _ := config["model_providers"].(map[string]interface{})
	zen, _ := providers["opencode-zen"].(map[string]interface{})
	if zen["base_url"] != "http://127.0.0.1:2026/codex-opencode-zen/v1" || zen["experimental_bearer_token"] != DefaultGatewayAPIKey {
		t.Errorf("opencode-zen provider block = %#v", zen)
	}
	proxyCfg, err := LoadProxyConfig()
	if err != nil {
		t.Fatal(err)
	}
	proxyProvider := proxyCfg.Providers[CodexProxyProviderID("opencode-zen")]
	if proxyProvider == nil || proxyProvider.Upstream != "https://opencode.ai/zen" || proxyProvider.AuthToken != "sk-zen" {
		t.Errorf("Codex proxy provider = %#v", proxyProvider)
	}
}

func TestSwitchNativeProviderKeepsPreviousProviderConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("DEEPSEEK_API_KEY", "sk-deepseek")
	t.Setenv("OPENCODE_GO_API_KEY", "sk-go")

	app, err := ResolveApp("codex")
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyProviderWithModel(app, "deepseek", ""); err != nil {
		t.Fatalf("apply deepseek: %v", err)
	}
	if err := ApplyProviderWithModel(app, "opencode-go", "gpt-5.6-luna"); err != nil {
		t.Fatalf("apply opencode-go: %v", err)
	}

	config, err := readTomlMap(CodexConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	providers, _ := config["model_providers"].(map[string]interface{})
	if providers["deepseek"] == nil {
		t.Error("deepseek provider block must survive switching to opencode-go")
	}
	if providers["opencode-go"] == nil {
		t.Error("opencode-go provider block missing")
	}
	ds, _ := providers["deepseek"].(map[string]interface{})
	if ds["experimental_bearer_token"] != DefaultGatewayAPIKey {
		t.Errorf("deepseek gateway key not preserved: %#v", ds)
	}
	proxyCfg, err := LoadProxyConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got := proxyCfg.Providers[CodexProxyProviderID("deepseek")].AuthToken; got != "sk-deepseek" {
		t.Errorf("deepseek upstream key = %q, want sk-deepseek", got)
	}
}

func TestApplyOpenCodeGoWithAnyModel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENCODE_GO_API_KEY", "sk-go")

	app, err := ResolveApp("codex")
	if err != nil {
		t.Fatal(err)
	}
	// Mirrors: aix codex opencode-go deepseek-v4-flash
	if err := ApplyProviderWithModel(app, "opencode-go", "deepseek-v4-flash"); err != nil {
		t.Fatalf("ApplyProviderWithModel: %v", err)
	}
	config, err := readTomlMap(CodexConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if config["model"] != "deepseek-v4-flash" || config["model_provider"] != "opencode-go" {
		t.Errorf("config = %#v", config)
	}
	var catalog struct {
		Models []struct {
			Slug string `json:"slug"`
		} `json:"models"`
	}
	raw, err := os.ReadFile(CodexModelsPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		t.Fatal(err)
	}
	slugs := make(map[string]bool)
	for _, m := range catalog.Models {
		slugs[m.Slug] = true
	}
	if !slugs["deepseek-v4-flash"] || !slugs["gpt-5.6-luna"] {
		t.Errorf("catalog missing active/curated models: %v", slugs)
	}
}

func TestMinimalCatalogEntriesCarryDesktopEffortFields(t *testing.T) {
	// Arbitrary AllowAnyModel overrides and user-defined native providers go
	// through minimalNativeCatalogMetadata. Codex 0.148+ desktop surfaces the
	// effort picker from these fields, so they must be present even when the
	// provider has no rich CatalogMetadata factory.
	path := filepath.Join(t.TempDir(), "models.json")
	if err := writeNativeModelCatalog("opencode-go", path, "my-custom-model"); err != nil {
		t.Fatalf("writeNativeModelCatalog: %v", err)
	}
	var catalog struct {
		Models []map[string]json.RawMessage `json:"models"`
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		t.Fatal(err)
	}
	var entry map[string]json.RawMessage
	for _, m := range catalog.Models {
		var slug string
		if err := json.Unmarshal(m["slug"], &slug); err != nil {
			t.Fatalf("decode slug: %v", err)
		}
		if slug == "my-custom-model" {
			entry = m
			break
		}
	}
	if entry == nil {
		t.Fatal("arbitrary active model missing from catalog")
	}
	var displayName string
	if err := json.Unmarshal(entry["display_name"], &displayName); err != nil {
		t.Fatalf("display_name: %v", err)
	}
	if displayName != "my-custom-model" {
		t.Errorf("display_name = %q, want slug fallback", displayName)
	}
	var visibility string
	if err := json.Unmarshal(entry["visibility"], &visibility); err != nil {
		t.Fatalf("visibility: %v", err)
	}
	if visibility != "list" {
		t.Errorf("visibility = %q, want list", visibility)
	}
	var defaultLevel string
	if err := json.Unmarshal(entry["default_reasoning_level"], &defaultLevel); err != nil {
		t.Fatalf("default_reasoning_level: %v", err)
	}
	if defaultLevel != "high" {
		t.Errorf("default_reasoning_level = %q, want high", defaultLevel)
	}
	var levels []struct {
		Effort string `json:"effort"`
	}
	if err := json.Unmarshal(entry["supported_reasoning_levels"], &levels); err != nil {
		t.Fatalf("supported_reasoning_levels: %v", err)
	}
	efforts := make(map[string]bool)
	for _, l := range levels {
		efforts[l.Effort] = true
	}
	for _, want := range []string{"low", "high", "max"} {
		if !efforts[want] {
			t.Errorf("supported_reasoning_levels missing %q: %v", want, efforts)
		}
	}
}

func TestWriteLiveModelCatalogWritesAllRemoteModels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	if err := os.WriteFile(path, []byte(`{"keep":true,"models":[{"slug":"old"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	spec := NativeProviderSpecs()["opencode-go"]
	remote := []RemoteModel{
		{ID: "glm-5.2", Name: "GLM 5.2"},
		{ID: "glm-5.3", Name: "GLM 5.3"},
		{ID: "hy3", Name: "Hy3", ContextWindow: 512000},
	}
	if err := writeLiveModelCatalog(spec, path, DeepSeekV4FlashModel, remote); err != nil {
		t.Fatalf("writeLiveModelCatalog: %v", err)
	}
	var catalog struct {
		Keep   bool                         `json:"keep"`
		Models []map[string]json.RawMessage `json:"models"`
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		t.Fatal(err)
	}
	if !catalog.Keep {
		t.Error("top-level catalog keys were not preserved")
	}
	if len(catalog.Models) != 4 {
		t.Fatalf("got %d catalog entries, want 4 (3 live + active model)", len(catalog.Models))
	}
	bySlug := map[string]map[string]json.RawMessage{}
	for _, m := range catalog.Models {
		var slug string
		if err := json.Unmarshal(m["slug"], &slug); err != nil {
			t.Fatalf("decode slug: %v", err)
		}
		bySlug[slug] = m
	}
	for _, slug := range []string{"glm-5.2", "glm-5.3", "hy3", DeepSeekV4FlashModel} {
		if _, ok := bySlug[slug]; !ok {
			t.Fatalf("model %q missing from synced catalog", slug)
		}
	}
	var display string
	if err := json.Unmarshal(bySlug["glm-5.2"]["display_name"], &display); err != nil {
		t.Fatal(err)
	}
	if display != "GLM 5.2" {
		t.Errorf("glm-5.2 display_name = %q, want curated %q", display, "GLM 5.2")
	}
	if err := json.Unmarshal(bySlug["glm-5.3"]["display_name"], &display); err != nil {
		t.Fatal(err)
	}
	if display != "glm-5.3" {
		t.Errorf("glm-5.3 display_name = %q, want slug fallback", display)
	}
	var ctx int
	if err := json.Unmarshal(bySlug["hy3"]["context_window"], &ctx); err != nil {
		t.Fatal(err)
	}
	if ctx != 512000 {
		t.Errorf("hy3 context_window = %d, want live 512000", ctx)
	}
	for slug, m := range bySlug {
		for _, key := range []string{"display_name", "default_reasoning_level", "supported_reasoning_levels", "visibility"} {
			if _, present := m[key]; !present {
				t.Errorf("%s entry missing %q", slug, key)
			}
		}
	}
}

func TestSyncLiveModelCatalogFiltersUnverifiedModels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	spec := NativeProviderSpecs()["opencode-go"]
	remote := []RemoteModel{
		{ID: "deepseek-v4-flash-vision-exp", Name: "DeepSeek V4 Flash Vision Exp"},
		{ID: "deepseek-v4-flash", Name: "DeepSeek V4 Flash"},
		{ID: "glm-5.3", Name: "GLM 5.3"},
		{ID: "hy3", Name: "Hy3"},
	}
	count, err := syncLiveModelCatalog(spec, path, "gpt-5.6-luna", remote)
	if err != nil {
		t.Fatalf("syncLiveModelCatalog: %v", err)
	}
	if count != 3 {
		t.Fatalf("verified count = %d, want 3", count)
	}
	var catalog struct {
		Models []struct {
			Slug string `json:"slug"`
		} `json:"models"`
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool, len(catalog.Models))
	for _, m := range catalog.Models {
		seen[m.Slug] = true
	}
	for _, want := range []string{"deepseek-v4-flash-vision-exp", "deepseek-v4-flash", "gpt-5.6-luna"} {
		if !seen[want] {
			t.Errorf("verified model %q missing from synced catalog", want)
		}
	}
	for _, unwanted := range []string{"glm-5.3", "hy3", "grok-4.5"} {
		if seen[unwanted] {
			t.Errorf("unverified model %q must not be synced", unwanted)
		}
	}
}

func TestSyncLiveModelCatalogRejectsAllUnverifiedModels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	spec := NativeProviderSpecs()["opencode-go"]
	if _, err := syncLiveModelCatalog(spec, path, "", []RemoteModel{{ID: "glm-5.3"}}); err == nil {
		t.Fatal("expected all-unverified live catalog to be rejected")
	}
}

func TestApplyOpenRouterNormalizesBareModel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENROUTER_API_KEY", "sk-or")

	app, err := ResolveApp("codex")
	if err != nil {
		t.Fatal(err)
	}
	// Mirrors: aix codex openrouter deepseek-v4-flash-latest
	if err := ApplyProviderWithModel(app, "openrouter", "deepseek-v4-flash-latest"); err != nil {
		t.Fatalf("ApplyProviderWithModel: %v", err)
	}
	config, err := readTomlMap(CodexConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if config["model"] != "~deepseek/deepseek-v4-flash-latest" || config["model_provider"] != "openrouter" {
		t.Errorf("config = %#v", config)
	}
	var catalog struct {
		Models []struct {
			Slug string `json:"slug"`
		} `json:"models"`
	}
	raw, err := os.ReadFile(CodexModelsPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		t.Fatal(err)
	}
	slugs := make(map[string]bool)
	for _, m := range catalog.Models {
		slugs[m.Slug] = true
	}
	if !slugs["~deepseek/deepseek-v4-flash-latest"] || !slugs["deepseek/deepseek-v4-pro"] {
		t.Errorf("catalog missing active/curated models: %v", slugs)
	}
}

func TestFetchRemoteModels(t *testing.T) {
	// OpenCode-style minimal payload.
	minimal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"object":"list","data":[{"id":"gpt-5.6-luna"},{"id":"kimi-k2.7-code"},{"id":"deepseek-v4-pro"}]}`))
	}))
	defer minimal.Close()
	models, err := fetchRemoteModels(minimal.URL, 2*time.Second)
	if err != nil {
		t.Fatalf("fetchRemoteModels: %v", err)
	}
	if len(models) != 3 || models[0].ID != "gpt-5.6-luna" || models[0].Name != "gpt-5.6-luna" {
		t.Errorf("minimal models = %+v", models)
	}

	// OpenRouter-style rich payload.
	rich := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"object":"list","data":[{"id":"openai/gpt-5.3-codex","name":"GPT 5.3 Codex","context_length":400000},{"id":"meta/muse-spark-1.2","name":"Meta: Muse Spark 1.2","context_length":1048576}]}`))
	}))
	defer rich.Close()
	models, err = fetchRemoteModels(rich.URL, 2*time.Second)
	if err != nil {
		t.Fatalf("fetchRemoteModels rich: %v", err)
	}
	if len(models) != 2 || models[1].Name != "Meta: Muse Spark 1.2" || models[1].ContextWindow != 1048576 {
		t.Errorf("rich models = %+v", models)
	}

	// Errors surface as fetch failures, not panics.
	if _, err := fetchRemoteModels("http://127.0.0.1:1/models", time.Second); err == nil {
		t.Error("unreachable endpoint should fail")
	}
	if _, err := FetchNativeProviderModels("nope", time.Second); err == nil {
		t.Error("unknown provider should fail")
	}
}

func TestIsNativeModel(t *testing.T) {
	if !IsNativeModel("opencode-go", "gpt-5.6-luna") {
		t.Error("gpt-5.6-luna should be a native opencode-go model")
	}
	if !IsNativeModel("opencode-go", "kimi-k2.7-code") {
		t.Error("kimi-k2.7-code should be in the curated opencode-go recommendations")
	}
	if !IsNativeModel("openrouter", "deepseek/deepseek-v4-pro") {
		t.Error("curated openrouter DeepSeek model should be native")
	}
	if !IsNativeModel("openrouter", "~deepseek/deepseek-v4-flash-latest") {
		t.Error("curated openrouter latest model should be native")
	}
	if IsNativeModel("openrouter", "openai/gpt-5.3-codex") {
		t.Error("OpenAI openrouter slug should no longer be native")
	}
	if IsNativeModel("openrouter", "meta/muse-spark-1.2") {
		t.Error("non-curated openrouter model must not be marked native")
	}
}

func TestResolveNativeModel(t *testing.T) {
	if got, err := ResolveNativeModel("deepseek", ""); err != nil || got != DeepSeekV4VisionModel {
		t.Fatalf("default = (%q, %v)", got, err)
	}
	if got, err := ResolveNativeModel("deepseek", DeepSeekV4FlashModel); err != nil || got != DeepSeekV4FlashModel {
		t.Fatalf("flash = (%q, %v)", got, err)
	}
	if _, err := ResolveNativeModel("deepseek", "gpt-5.5"); err == nil {
		t.Error("invalid model should be rejected")
	}
	if _, err := ResolveNativeModel("kimi", ""); err == nil {
		t.Error("unregistered provider should be rejected")
	}
}

func TestRemoveAllCodexNativeModels(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.json")
	if err := os.WriteFile(path, []byte(`{"models":[{"slug":"deepseek-v4-flash"},{"slug":"deepseek-v4-pro"},{"slug":"other-model"}],"keep":true}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := RemoveAllCodexNativeModels(path); err != nil {
		t.Fatalf("RemoveAllCodexNativeModels: %v", err)
	}
	var catalog struct {
		Models []struct {
			Slug string `json:"slug"`
		} `json:"models"`
		Keep bool `json:"keep"`
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog.Models) != 1 || catalog.Models[0].Slug != "other-model" || !catalog.Keep {
		t.Errorf("catalog after removal = %+v", catalog)
	}
}

func TestSetCodexProviderLogin(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	content := `model = "deepseek-v4-flash"
model_provider = "deepseek"

[model_providers.deepseek]
name = "deepseek"
base_url = "https://api.deepseek.com/"
wire_api = "responses"
`
	if err := os.WriteFile(configPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	opts := CodexLoginOptions{Login: "LIZHICHAO", ConfigPath: configPath}
	if err := SetCodexProviderLoginAt(opts); err != nil {
		t.Fatalf("SetCodexProviderLoginAt: %v", err)
	}
	config, err := readTomlMap(configPath)
	if err != nil {
		t.Fatal(err)
	}
	providers := config["model_providers"].(map[string]interface{})
	ds := providers["deepseek"].(map[string]interface{})
	if ds["name"] != "LIZHICHAO" {
		t.Errorf("name = %v, want LIZHICHAO", ds["name"])
	}
	if config["model"] != "deepseek-v4-flash" || config["model_provider"] != "deepseek" {
		t.Errorf("unrelated config was changed: %v", config)
	}

	// Empty value clears the label.
	if err := SetCodexProviderLoginAt(CodexLoginOptions{ConfigPath: configPath}); err != nil {
		t.Fatalf("clear login: %v", err)
	}
	config, _ = readTomlMap(configPath)
	providers = config["model_providers"].(map[string]interface{})
	ds = providers["deepseek"].(map[string]interface{})
	if _, ok := ds["name"]; ok {
		t.Errorf("name should be removed after clear: %v", ds)
	}
}

func TestSetCodexProviderLogin_DefaultModeRejected(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(configPath, []byte("model = \"gpt-5.5\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := SetCodexProviderLoginAt(CodexLoginOptions{Login: "X", ConfigPath: configPath}); err == nil {
		t.Error("default GPT mode should reject login changes")
	}
}

func TestConfigureCodexDeepSeekAtReplacesCatalogWithProviderModels(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	catalogPath := filepath.Join(dir, "models.json")
	if err := os.WriteFile(configPath, []byte(`personality = "pragmatic"

[mcp_servers.example]
command = "example"

[model_providers.other]
name = "Other"
base_url = "https://example.test"
`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(catalogPath, []byte("{\"models\":[{\"slug\":\"other-model\"},{\"slug\":\"deepseek-v4-flash\",\"display_name\":\"old\"}],\"keep\":true}\n"), 0600); err != nil {
		t.Fatal(err)
	}

	opts := CodexDeepSeekOptions{
		APIKey:           "sk-test",
		Model:            DeepSeekV4ProModel,
		ConfigPath:       configPath,
		ModelCatalogPath: catalogPath,
		BackupDir:        filepath.Join(dir, "backups"),
	}
	if err := ConfigureCodexDeepSeekAt(opts); err != nil {
		t.Fatalf("ConfigureCodexDeepSeekAt: %v", err)
	}

	var config map[string]interface{}
	rawConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := toml.Decode(string(rawConfig), &config); err != nil {
		t.Fatal(err)
	}
	if config["model"] != DeepSeekV4ProModel || config["model_provider"] != "deepseek" {
		t.Fatalf("unexpected model config: %#v", config)
	}
	if config["model_catalog_json"] != catalogPath || config["personality"] != "pragmatic" || config["mcp_servers"] == nil {
		t.Errorf("existing Codex settings were not preserved: %#v", config)
	}
	providers := config["model_providers"].(map[string]interface{})
	if providers["other"] == nil || providers["deepseek"] == nil {
		t.Errorf("providers were not merged: %#v", providers)
	}
	deepseek := providers["deepseek"].(map[string]interface{})
	if deepseek["wire_api"] != "responses" || deepseek["experimental_bearer_token"] != "sk-test" {
		t.Errorf("unexpected DeepSeek provider: %#v", deepseek)
	}
	if info, err := os.Stat(configPath); err != nil || info.Mode().Perm() != 0600 {
		t.Errorf("config mode = %v, err = %v; want 0600", info.Mode().Perm(), err)
	}

	var catalog struct {
		Models []struct {
			Slug        string `json:"slug"`
			DisplayName string `json:"display_name"`
		} `json:"models"`
		Keep bool `json:"keep"`
	}
	rawCatalog, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(rawCatalog, &catalog); err != nil {
		t.Fatal(err)
	}
	if !catalog.Keep || len(catalog.Models) != 3 {
		t.Fatalf("catalog after switch = %#v, want only the DeepSeek models", catalog)
	}
	seen := make(map[string]bool)
	for _, model := range catalog.Models {
		seen[model.Slug] = true
	}
	for _, model := range []string{DeepSeekV4FlashModel, DeepSeekV4ProModel, DeepSeekV4VisionModel} {
		if !seen[model] {
			t.Errorf("model %q missing from catalog", model)
		}
	}
	if seen["other-model"] {
		t.Error("other-model from a different provider should be removed from catalog")
	}
	for _, model := range catalog.Models {
		if model.Slug == DeepSeekV4FlashModel && model.DisplayName == "old" {
			t.Error("stale deepseek-v4-flash entry was not replaced with fresh metadata")
		}
	}
}

func TestSwitchFromOpenCodeGoToDeepSeekDropsOtherProviderCatalogModels(t *testing.T) {
	dir := t.TempDir()
	catalogPath := filepath.Join(dir, "models.json")
	// Mirrors the state left by `aix codex opencode-go gpt-5.6-luna`: the
	// OpenCode Go catalog includes models that DeepSeek cannot serve.
	if err := os.WriteFile(catalogPath, []byte(`{"models":[{"slug":"gpt-5.6-luna"},{"slug":"kimi-k2.7-code"},{"slug":"glm-5.2"},{"slug":"qwen3.8-max"},{"slug":"minimax-m3"},{"slug":"deepseek-v4-flash"},{"slug":"deepseek-v4-pro"}],"keep":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ConfigureCodexDeepSeekAt(CodexDeepSeekOptions{
		APIKey:           "sk-test",
		Model:            DeepSeekV4FlashModel,
		ConfigPath:       filepath.Join(dir, "config.toml"),
		ModelCatalogPath: catalogPath,
		BackupDir:        filepath.Join(dir, "backups"),
	}); err != nil {
		t.Fatalf("ConfigureCodexDeepSeekAt: %v", err)
	}
	var catalog struct {
		Models []struct {
			Slug string `json:"slug"`
		} `json:"models"`
		Keep bool `json:"keep"`
	}
	raw, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		t.Fatal(err)
	}
	if !catalog.Keep || len(catalog.Models) != 3 {
		t.Fatalf("catalog after switch = %#v, want only the DeepSeek models", catalog)
	}
	seen := make(map[string]bool)
	for _, model := range catalog.Models {
		seen[model.Slug] = true
	}
	for _, model := range []string{DeepSeekV4FlashModel, DeepSeekV4ProModel, DeepSeekV4VisionModel} {
		if !seen[model] {
			t.Errorf("model %q missing from catalog", model)
		}
	}
	for _, stale := range []string{"gpt-5.6-luna", "kimi-k2.7-code", "glm-5.2", "qwen3.8-max", "minimax-m3"} {
		if seen[stale] {
			t.Errorf("stale model %q should be removed from catalog", stale)
		}
	}
}

func TestWriteDeepSeekCatalogSourcesBundledFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	if err := writeNativeModelCatalog("deepseek", path, DeepSeekV4FlashModel); err != nil {
		t.Fatalf("writeNativeModelCatalog: %v", err)
	}
	bundled, err := BundledDeepSeekCatalog()
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Models []map[string]json.RawMessage `json:"models"`
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog.Models) != len(bundled) {
		t.Fatalf("written %d models, bundled catalog has %d", len(catalog.Models), len(bundled))
	}
	for _, m := range catalog.Models {
		var slug, displayName string
		if err := json.Unmarshal(m["slug"], &slug); err != nil {
			t.Fatalf("decode slug: %v", err)
		}
		if err := json.Unmarshal(m["display_name"], &displayName); err != nil {
			t.Fatalf("decode display_name: %v", err)
		}
		entry, ok := bundled[slug]
		if !ok {
			t.Fatalf("model %q not in bundled catalog", slug)
		}
		bundledName, _ := entry["display_name"].(string)
		if displayName != bundledName {
			t.Errorf("%s display_name = %q, want bundled %q", slug, displayName, bundledName)
		}
	}
}

func TestWriteDeepSeekCatalogPrefersLiveRefresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	official, err := BundledDeepSeekCatalog()
	if err != nil {
		t.Fatal(err)
	}
	live := make(map[string]map[string]interface{}, len(official))
	for slug, entry := range official {
		clone := make(map[string]interface{}, len(entry))
		for k, v := range entry {
			clone[k] = v
		}
		clone["display_name"] = "Live " + slug
		live[slug] = clone
	}
	deepSeekCatalogCache = live
	defer func() { deepSeekCatalogCache = nil }()
	if err := writeNativeModelCatalog("deepseek", path, ""); err != nil {
		t.Fatalf("writeNativeModelCatalog: %v", err)
	}
	var catalog struct {
		Models []map[string]json.RawMessage `json:"models"`
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool, len(live))
	for _, m := range catalog.Models {
		var slug, displayName string
		if err := json.Unmarshal(m["slug"], &slug); err != nil {
			t.Fatalf("decode slug: %v", err)
		}
		if err := json.Unmarshal(m["display_name"], &displayName); err != nil {
			t.Fatalf("decode display_name: %v", err)
		}
		seen[slug] = true
		if displayName != "Live "+slug {
			t.Errorf("%s display_name = %q, want live value", slug, displayName)
		}
	}
	for slug := range live {
		if !seen[slug] {
			t.Errorf("live model %q missing from written catalog", slug)
		}
	}
}

func TestRemoveCodexDeepSeekModelsPreservesOtherModels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	if err := os.WriteFile(path, []byte(`{"models":[{"slug":"other"},{"slug":"deepseek-v4-flash"},{"slug":"deepseek-v4-pro"}],"keep":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := RemoveCodexDeepSeekModels(path); err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Models []struct {
			Slug string `json:"slug"`
		} `json:"models"`
		Keep bool `json:"keep"`
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		t.Fatal(err)
	}
	if !catalog.Keep || len(catalog.Models) != 1 || catalog.Models[0].Slug != "other" {
		t.Errorf("unexpected catalog after removal: %#v", catalog)
	}
}

func TestRestoreCodexNativeAtRemovesDeepSeekSettings(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	catalogPath := filepath.Join(dir, "models.json")
	config := `model = "deepseek-v4-flash"
model_provider = "deepseek"
preferred_auth_method = "apikey"
forced_login_method = "api"
model_reasoning_effort = "high"
model_catalog_json = "/tmp/models.json"
personality = "pragmatic"

[model_providers.deepseek]
base_url = "https://api.deepseek.com"
`
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(catalogPath, []byte(`{"models":[{"slug":"other"},{"slug":"deepseek-v4-flash"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := restoreCodexNativeAt(configPath, catalogPath, filepath.Join(dir, "backups")); err != nil {
		t.Fatal(err)
	}

	var restored map[string]interface{}
	rawConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := toml.Decode(string(rawConfig), &restored); err != nil {
		t.Fatal(err)
	}
	if restored["personality"] != "pragmatic" {
		t.Errorf("personality = %q, want pragmatic", restored["personality"])
	}
	for _, key := range []string{"model_providers", "preferred_auth_method", "forced_login_method", "model_reasoning_effort", "model_catalog_json"} {
		if _, ok := restored[key]; ok {
			t.Errorf("%s was not removed", key)
		}
	}
	if restored["model_provider"] != "openai" {
		t.Errorf("model_provider = %v, want openai", restored["model_provider"])
	}
	if restored["model"] != DefaultOpenAICodexModel {
		t.Errorf("model = %v, want %q", restored["model"], DefaultOpenAICodexModel)
	}
	if _, err := os.Stat(catalogPath); !os.IsNotExist(err) {
		t.Errorf("model catalog should be removed on restore (stat err = %v)", err)
	}
}

func TestCodexNativeSnapshotSurvivesManagedSwitchesAndRestoresSelection(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	catalogPath := filepath.Join(dir, "models.json")
	backupDir := filepath.Join(dir, "backups")
	originalCatalog := []byte(`{"models":[{"slug":"native-custom"}]}` + "\n")
	config := `model = "gpt-5.5"
model_provider = "openai"
model_reasoning_effort = "xhigh"
preferred_auth_method = "chatgpt"
personality = "friendly"

[model_providers.openai-custom]
name = "My OpenAI"
base_url = "https://example.invalid/v1"
wire_api = "responses"
`
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(catalogPath, originalCatalog, 0600); err != nil {
		t.Fatal(err)
	}

	first := CodexNativeOptions{
		ProviderID:       "deepseek",
		APIKey:           "first-key",
		Model:            DeepSeekV4FlashModel,
		Effort:           "high",
		ConfigPath:       configPath,
		ModelCatalogPath: catalogPath,
		BackupDir:        backupDir,
	}
	if err := ConfigureCodexNativeAt(first); err != nil {
		t.Fatal(err)
	}
	snapshotPath := codexNativeSnapshotPath(backupDir)
	firstSnapshot, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}

	managed, err := readTomlMap(configPath)
	if err != nil {
		t.Fatal(err)
	}
	managed["personality"] = "pragmatic"
	if err := writeTomlPrivate(configPath, managed); err != nil {
		t.Fatal(err)
	}
	second := first
	second.ProviderID = "opencode-go"
	second.APIKey = "second-key"
	second.Model = "gpt-5.6-luna"
	if err := ConfigureCodexNativeAt(second); err != nil {
		t.Fatal(err)
	}
	secondSnapshot, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstSnapshot, secondSnapshot) {
		t.Fatal("managed provider switch overwrote the original native snapshot")
	}

	if err := restoreCodexNativeAt(configPath, catalogPath, backupDir); err != nil {
		t.Fatal(err)
	}
	restored, err := readTomlMap(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if restored["model_provider"] != "openai" || restored["model"] != "gpt-5.5" {
		t.Errorf("native selection not restored: provider=%v model=%v", restored["model_provider"], restored["model"])
	}
	if restored["model_reasoning_effort"] != "xhigh" {
		t.Errorf("native effort = %v, want xhigh", restored["model_reasoning_effort"])
	}
	if restored["preferred_auth_method"] != "chatgpt" {
		t.Errorf("native auth = %v, want chatgpt", restored["preferred_auth_method"])
	}
	if restored["personality"] != "pragmatic" {
		t.Errorf("unrelated managed-period change was lost: personality=%v", restored["personality"])
	}
	providers, _ := restored["model_providers"].(map[string]interface{})
	if _, ok := providers["openai-custom"]; !ok {
		t.Errorf("original model provider config not restored: %#v", providers)
	}
	if raw, err := os.ReadFile(catalogPath); err != nil || !bytes.Equal(raw, originalCatalog) {
		t.Errorf("native catalog not restored: %q, %v", raw, err)
	}
	if _, err := os.Stat(snapshotPath); !os.IsNotExist(err) {
		t.Errorf("native snapshot should be consumed after restore: %v", err)
	}
}

func TestRestoreCodexNativeAtRemovesSyncedUnknownModels(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	catalogPath := filepath.Join(dir, "models.json")
	if err := os.WriteFile(configPath, []byte("model = \"gpt-5.6-luna\"\nmodel_provider = \"opencode-go\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// These IDs came from a live catalog and are intentionally absent from the
	// static provider registry, reproducing the restore regression.
	if err := os.WriteFile(catalogPath, []byte(`{"models":[{"slug":"glm-5.3"},{"slug":"kimi-k2.6"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	backupDir := filepath.Join(dir, "backups")
	if err := restoreCodexNativeAt(configPath, catalogPath, backupDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(catalogPath); !os.IsNotExist(err) {
		t.Errorf("synced catalog should be removed on restore (stat err = %v)", err)
	}
	backups, err := filepath.Glob(filepath.Join(backupDir, "models.json.native.*.bak"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("catalog backup count = %d, err = %v", len(backups), err)
	}
}

func TestConfigureCodexDeepSeekAtRejectsUnknownModelOrMissingKey(t *testing.T) {
	dir := t.TempDir()
	base := CodexDeepSeekOptions{
		ConfigPath:       filepath.Join(dir, "config.toml"),
		ModelCatalogPath: filepath.Join(dir, "models.json"),
		BackupDir:        filepath.Join(dir, "backups"),
	}
	if err := ConfigureCodexDeepSeekAt(base); err == nil {
		t.Error("accepted an empty model and key")
	}
	base.Model = DeepSeekV4FlashModel
	if err := ConfigureCodexDeepSeekAt(base); err == nil {
		t.Error("accepted a missing API key")
	}
	base.APIKey = "sk-test"
	base.Model = "other-model"
	if err := ConfigureCodexDeepSeekAt(base); err == nil {
		t.Error("accepted an unsupported model")
	}
}

// TestCodexDeepSeekCatalogMatchesCodexSchema guards against the regression
// where Codex 0.147+ rejects ~/.codex/models.json with
// "missing field `base_instructions`". The parser now requires the field on
// every model entry, so both deepseek-v4-flash and deepseek-v4-pro must
// carry AIX's complete system-prompt payload plus all capability fields
// published by DeepSeek's official setup script.
func TestCodexDeepSeekCatalogMatchesCodexSchema(t *testing.T) {
	dir := t.TempDir()
	catalogPath := filepath.Join(dir, "models.json")
	if err := ConfigureCodexDeepSeekAt(CodexDeepSeekOptions{
		APIKey:           "sk-test",
		Model:            DeepSeekV4FlashModel,
		ConfigPath:       filepath.Join(dir, "config.toml"),
		ModelCatalogPath: catalogPath,
		BackupDir:        filepath.Join(dir, "backups"),
	}); err != nil {
		t.Fatalf("ConfigureCodexDeepSeekAt: %v", err)
	}

	var catalog struct {
		Models []map[string]json.RawMessage `json:"models"`
	}
	raw, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		t.Fatalf("catalog is not valid JSON: %v", err)
	}

	wantBySlug := map[string]string{
		DeepSeekV4FlashModel:  "DeepSeek-V4-Flash",
		DeepSeekV4ProModel:    "DeepSeek-V4-Pro",
		DeepSeekV4VisionModel: "DeepSeek-V4-Flash-Vision",
	}
	seen := make(map[string]bool)
	for _, entry := range catalog.Models {
		var slug string
		if err := json.Unmarshal(entry["slug"], &slug); err != nil {
			t.Fatalf("decode slug: %v", err)
		}
		wantName, ok := wantBySlug[slug]
		if !ok {
			continue
		}
		seen[slug] = true

		// Fields the Codex 0.147 deserializer requires on every entry.
		required := []string{
			"base_instructions",
			"model_messages",
			"slug",
			"display_name",
			"description",
			"priority",
			"supported_reasoning_levels",
			"context_window",
			"max_context_window",
			"truncation_policy",
			"input_modalities",
			"shell_type",
			"visibility",
			"supported_in_api",
			"minimal_client_version",
		}
		for _, key := range required {
			if _, present := entry[key]; !present {
				t.Errorf("%s: missing required field %q", slug, key)
			}
		}

		// base_instructions must be a non-empty string equal to the embedded
		// Codex system prompt, and must match model_messages.instructions_template
		// (DeepSeek's official catalog embeds the same payload in both places).
		var baseInstructions string
		if err := json.Unmarshal(entry["base_instructions"], &baseInstructions); err != nil {
			t.Fatalf("%s: base_instructions is not a string: %v", slug, err)
		}
		if baseInstructions != codexBaseInstructions {
			t.Errorf("%s: base_instructions does not match embedded asset (got len=%d, want len=%d)", slug, len(baseInstructions), len(codexBaseInstructions))
		}
		if len(baseInstructions) < 1000 {
			t.Errorf("%s: base_instructions is suspiciously short (len=%d)", slug, len(baseInstructions))
		}

		var modelMessages struct {
			InstructionsTemplate  string            `json:"instructions_template"`
			InstructionsVariables map[string]string `json:"instructions_variables"`
			Approvals             *json.RawMessage  `json:"approvals"`
		}
		if err := json.Unmarshal(entry["model_messages"], &modelMessages); err != nil {
			t.Fatalf("%s: model_messages parse error: %v", slug, err)
		}
		if modelMessages.InstructionsTemplate != baseInstructions {
			t.Errorf("%s: model_messages.instructions_template differs from base_instructions", slug)
		}
		for _, key := range []string{"personality_default", "personality_friendly", "personality_pragmatic"} {
			if v, ok := modelMessages.InstructionsVariables[key]; !ok || v != "" {
				t.Errorf("%s: instructions_variables.%s = %q, want \"\"", slug, key, v)
			}
		}

		var displayName string
		if err := json.Unmarshal(entry["display_name"], &displayName); err != nil {
			t.Fatalf("%s: display_name parse error: %v", slug, err)
		}
		if displayName != wantName {
			t.Errorf("%s: display_name = %q, want %q", slug, displayName, wantName)
		}
	}
	for slug := range wantBySlug {
		if !seen[slug] {
			t.Errorf("catalog is missing entry for %q", slug)
		}
	}
}
