package internal

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

//go:embed assets/codex_base_instructions.txt
var codexBaseInstructions string

const (
	DeepSeekV4FlashModel  = "deepseek-v4-flash"
	DeepSeekV4ProModel    = "deepseek-v4-pro"
	DeepSeekV4VisionModel = "deepseek-v4-flash-vision-exp"
)

// DefaultOpenAICodexModel is the model id recorded in ~/.codex/config.toml
// when AIX restores Codex to its default native OpenAI provider. Codex reads
// model_provider + model together; leaving model empty would let the host pick
// one, but retagging conversation history to openai needs a concrete,
// user-visible id so restored sessions load under the ChatGPT account.
//
// It is the canonical Codex default slug for the gpt-5.6 family, not the bare
// "gpt-5.6" family alias. Codex's OpenAI provider catalog lists only
// gpt-5.6-sol / -terra / -luna (plus older gpt-5.x slugs); the bare alias
// resolves at config-load time but is not a catalog slug, so a thread whose
// model was retagged to it fails on resume with "model is not supported".
const DefaultOpenAICodexModel = "gpt-5.6-sol"

// deepSeekV4ContextWindow is the documented native context window of the
// DeepSeek V4 family in tokens.
const deepSeekV4ContextWindow = 1048576

// NativeModelSpec describes one model in a native provider's Codex catalog.
type NativeModelSpec struct {
	Slug          string
	DisplayName   string
	Description   string
	Priority      int
	ContextWindow int
}

// NativeProviderSpec describes a provider that exposes a native Responses API
// for passthrough through the AIX gateway. Adding one is a single registry
// entry plus an optional catalog metadata factory.
type NativeProviderSpec struct {
	ID              string
	Name            string
	EnvKey          string
	EnvKeyAliases   []string
	BaseURL         string
	DefaultModel    string
	Models          []NativeModelSpec
	AllowAnyModel   bool
	CatalogMetadata func(model string) map[string]interface{}
}

// openCodeZenModels are the OpenCode Zen models served through the Responses
// API (https://opencode.ai/zen/v1/responses), the only ones usable by Codex's
// native wire_api = "responses". Deprecated models are excluded.
var openCodeZenModels = []NativeModelSpec{
	{Slug: "deepseek-v4-pro", DisplayName: "DeepSeek V4 Pro", Description: "DeepSeek's most capable coding model served by OpenCode Zen.", Priority: 1, ContextWindow: deepSeekV4ContextWindow},
	{Slug: "deepseek-v4-flash", DisplayName: "DeepSeek V4 Flash", Description: "DeepSeek's fast agentic coding model served by OpenCode Zen.", Priority: 2, ContextWindow: deepSeekV4ContextWindow},
	{Slug: "deepseek-v4-flash-free", DisplayName: "DeepSeek V4 Flash Free", Description: "Free tier of DeepSeek's agentic coding model served by OpenCode Zen.", Priority: 3, ContextWindow: deepSeekV4ContextWindow},
	{Slug: "gpt-5.6-sol", DisplayName: "GPT 5.6 Sol", Description: "OpenAI frontier reasoning model served by OpenCode Zen.", Priority: 1},
	{Slug: "gpt-5.6-terra", DisplayName: "GPT 5.6 Terra", Description: "Balanced OpenAI reasoning model served by OpenCode Zen.", Priority: 2},
	{Slug: "gpt-5.6-luna", DisplayName: "GPT 5.6 Luna", Description: "Low-cost OpenAI reasoning model served by OpenCode Zen.", Priority: 3},
	{Slug: "gpt-5.5", DisplayName: "GPT 5.5", Description: "OpenAI frontier model served by OpenCode Zen.", Priority: 4},
	{Slug: "gpt-5.5-pro", DisplayName: "GPT 5.5 Pro", Description: "OpenAI deep-reasoning flagship served by OpenCode Zen.", Priority: 5},
	{Slug: "gpt-5.4", DisplayName: "GPT 5.4", Description: "OpenAI general-purpose model served by OpenCode Zen.", Priority: 6},
	{Slug: "gpt-5.4-pro", DisplayName: "GPT 5.4 Pro", Description: "OpenAI deep-reasoning model served by OpenCode Zen.", Priority: 7},
	{Slug: "gpt-5.4-mini", DisplayName: "GPT 5.4 Mini", Description: "Fast OpenAI model served by OpenCode Zen.", Priority: 8},
	{Slug: "gpt-5.4-nano", DisplayName: "GPT 5.4 Nano", Description: "Lowest-cost OpenAI model served by OpenCode Zen.", Priority: 9},
	{Slug: "gpt-5.3-codex", DisplayName: "GPT 5.3 Codex", Description: "Agentic coding model tuned for Codex workflows.", Priority: 10},
	{Slug: "gpt-5.3-codex-spark", DisplayName: "GPT 5.3 Codex Spark", Description: "Faster Codex-tuned coding model for iterative edits.", Priority: 11},
	{Slug: "grok-4.5", DisplayName: "Grok 4.5", Description: "xAI's frontier reasoning model served by OpenCode Zen.", Priority: 12},
	{Slug: "grok-build-0.1", DisplayName: "Grok Build 0.1", Description: "xAI's agentic coding model served by OpenCode Zen.", Priority: 13},
}

// openCodeGoModels are curated recommendations for the OpenCode Go
// subscription. The gateway accepts arbitrary slugs for explicit requests,
// but only this verified list is eligible for the Codex picker sync.
var openCodeGoModels = []NativeModelSpec{
	{Slug: "gpt-5.6-luna", DisplayName: "GPT 5.6 Luna", Description: "Low-cost OpenAI reasoning model included with the OpenCode Go subscription.", Priority: 1},
	{Slug: "deepseek-v4-pro", DisplayName: "DeepSeek V4 Pro", Description: "DeepSeek's most capable coding model on the Go subscription.", Priority: 2, ContextWindow: deepSeekV4ContextWindow},
	{Slug: "deepseek-v4-flash", DisplayName: "DeepSeek V4 Flash", Description: "DeepSeek's fast agentic coding model on the Go subscription.", Priority: 3, ContextWindow: deepSeekV4ContextWindow},
	{Slug: "kimi-k2.7-code", DisplayName: "Kimi K2.7 Code", Description: "Moonshot's agentic coding model on the Go subscription.", Priority: 4},
	{Slug: "glm-5.2", DisplayName: "GLM 5.2", Description: "Zhipu's frontier coding model on the Go subscription.", Priority: 5},
	{Slug: "qwen3.8-max", DisplayName: "Qwen3.8 Max", Description: "Qwen's frontier model on the Go subscription.", Priority: 6},
	{Slug: "minimax-m3", DisplayName: "MiniMax M3", Description: "MiniMax's flagship model on the Go subscription.", Priority: 7},
	{Slug: DeepSeekV4VisionModel, DisplayName: "DeepSeek V4 Flash Vision Exp", Description: "DeepSeek's experimental vision model on the Go subscription.", Priority: 8, ContextWindow: deepSeekV4ContextWindow},
}

// openRouterModels are a curated subset of OpenRouter's model catalog, kept
// intentionally small because OpenRouter accepts any vendor/model slug. When
// AllowAnyModel is set, --model accepts any non-empty slug and the active
// model is added to the Codex catalog at apply time. Only DeepSeek models are
// curated; OpenAI and Anthropic slugs are intentionally not promoted here.
var openRouterModels = []NativeModelSpec{
	{Slug: "deepseek/deepseek-v4-pro", DisplayName: "DeepSeek V4 Pro", Description: "DeepSeek's most capable coding model routed through OpenRouter.", Priority: 1, ContextWindow: deepSeekV4ContextWindow},
	{Slug: "deepseek/deepseek-v4-flash", DisplayName: "DeepSeek V4 Flash", Description: "DeepSeek's fast agentic coding model routed through OpenRouter.", Priority: 2, ContextWindow: deepSeekV4ContextWindow},
	{Slug: "~deepseek/deepseek-v4-flash-latest", DisplayName: "DeepSeek V4 Flash Latest", Description: "DeepSeek's latest agentic coding model routed through OpenRouter.", Priority: 3, ContextWindow: deepSeekV4ContextWindow},
	{Slug: "deepseek/deepseek-v4-flash-vision-exp", DisplayName: "DeepSeek V4 Flash Vision Exp", Description: "DeepSeek's experimental vision model routed through OpenRouter.", Priority: 4, ContextWindow: deepSeekV4ContextWindow},
	{Slug: "stealth/ox-alpha", DisplayName: "Stealth Ox Alpha", Description: "Stealth's frontier reasoning model routed through OpenRouter.", Priority: 5},
}

// openRouterModelAliases maps model names users type into OpenRouter model
// IDs that the Responses API actually accepts. OpenRouter requires
// vendor-prefixed IDs, and the "latest" router alias additionally needs the
// "~" prefix that selects OpenRouter's any-provider routing: the plain
// deepseek/deepseek-v4-flash-latest ID is rejected with HTTP 400 on
// /responses even though it appears in the /models catalog. Bare names and
// the rejected prefixed variant both normalize to the working ID.
var openRouterModelAliases = map[string]string{
	"deepseek-v4-flash-latest":              "~deepseek/deepseek-v4-flash-latest",
	"deepseek/deepseek-v4-flash-latest":     "~deepseek/deepseek-v4-flash-latest",
	"deepseek-v4-flash":                     "deepseek/deepseek-v4-flash",
	"deepseek-v4-pro":                       "deepseek/deepseek-v4-pro",
	"deepseek-v4-flash-vision-exp":          "deepseek/deepseek-v4-flash-vision-exp",
	"deepseek/deepseek-v4-flash-vision-exp": "deepseek/deepseek-v4-flash-vision-exp",
	"ox-alpha":                              "stealth/ox-alpha",
	"stealth/ox-alpha":                      "stealth/ox-alpha",
}

// NativeProviderSpecs is the registry of providers supported in Codex native
// direct mode. Each entry fully describes the provider; nothing else in the
// codebase needs a provider-specific branch.
func NativeProviderSpecs() map[string]NativeProviderSpec {
	specs := map[string]NativeProviderSpec{
		"deepseek": {
			ID:           "deepseek",
			Name:         "DeepSeek",
			EnvKey:       "DEEPSEEK_API_KEY",
			BaseURL:      "https://api.deepseek.com/",
			DefaultModel: DeepSeekV4VisionModel,
			Models: []NativeModelSpec{
				{Slug: DeepSeekV4FlashModel, DisplayName: "DeepSeek-V4-Flash", Description: "Latest frontier agentic coding model.", Priority: 1},
				{Slug: DeepSeekV4ProModel, DisplayName: "DeepSeek-V4-Pro", Description: "Most capable frontier agentic coding model.", Priority: 2},
				{Slug: DeepSeekV4VisionModel, DisplayName: "DeepSeek-V4-Flash-Vision", Description: "Latest frontier agentic coding model with image input.", Priority: 3},
			},
			CatalogMetadata: func(model string) map[string]interface{} {
				return deepSeekV4Metadata(model, codexBaseInstructions)
			},
		},
		"opencode-zen": {
			ID:            "opencode-zen",
			Name:          "OpenCode Zen",
			EnvKey:        "OPENCODE_ZEN_API_KEY",
			EnvKeyAliases: []string{"OPENCODE_API_KEY"},
			BaseURL:       "https://opencode.ai/zen/v1",
			DefaultModel:  "deepseek-v4-flash",
			Models:        openCodeZenModels,
			CatalogMetadata: func(model string) map[string]interface{} {
				return modelCatalogMetadata(openCodeZenModels, codexBaseInstructions, 400000, model)
			},
		},
		"opencode-go": {
			ID:            "opencode-go",
			Name:          "OpenCode Go",
			EnvKey:        "OPENCODE_GO_API_KEY",
			EnvKeyAliases: []string{"OPENCODE_ZEN_API_KEY", "OPENCODE_API_KEY"},
			BaseURL:       "https://opencode.ai/zen/go/v1",
			DefaultModel:  "deepseek-v4-flash-vision-exp",
			Models:        openCodeGoModels,
			AllowAnyModel: true,
			CatalogMetadata: func(model string) map[string]interface{} {
				return modelCatalogMetadata(openCodeGoModels, codexBaseInstructions, 400000, model)
			},
		},
		"openrouter": {
			ID:            "openrouter",
			Name:          "OpenRouter",
			EnvKey:        "OPENROUTER_API_KEY",
			BaseURL:       "https://openrouter.ai/api/v1",
			DefaultModel:  "deepseek/deepseek-v4-flash-vision-exp",
			Models:        openRouterModels,
			AllowAnyModel: true,
			CatalogMetadata: func(model string) map[string]interface{} {
				return modelCatalogMetadata(openRouterModels, codexBaseInstructions, 400000, model)
			},
		},
	}
	if users, err := LoadUserNativeProviders(); err == nil {
		for _, u := range users {
			specs[u.ID] = userNativeSpec(u)
		}
	}
	return specs
}

func userNativeSpec(u UserNativeProvider) NativeProviderSpec {
	models := make([]NativeModelSpec, 0, len(u.Models))
	for _, m := range u.Models {
		if strings.TrimSpace(m) == "" {
			continue
		}
		models = append(models, NativeModelSpec{Slug: m, DisplayName: m})
	}
	return NativeProviderSpec{
		ID:           u.ID,
		Name:         u.Name,
		EnvKey:       u.EnvKey,
		BaseURL:      u.BaseURL,
		DefaultModel: u.DefaultModel,
		Models:       models,
	}
}

// NativeProvider returns the spec for a provider ID.
func NativeProvider(id string) (NativeProviderSpec, bool) {
	spec, ok := NativeProviderSpecs()[id]
	return spec, ok
}

// IsNativeProvider reports whether the provider is registered for Codex
// native direct mode.
func IsNativeProvider(id string) bool {
	_, ok := NativeProviderSpecs()[id]
	return ok
}

// NativeModels returns the supported model slugs for a native provider.
func NativeModels(id string) []string {
	spec, ok := NativeProviderSpecs()[id]
	if !ok {
		return nil
	}
	out := make([]string, 0, len(spec.Models))
	for _, m := range spec.Models {
		out = append(out, m.Slug)
	}
	return out
}

// IsNativeModel reports whether a model slug is in a native provider's
// curated Responses-capable catalog.
func IsNativeModel(providerID, model string) bool {
	spec, ok := NativeProvider(providerID)
	if !ok {
		return false
	}
	return nativeModelInList(spec, model)
}

// RemoteModel is one entry from a provider's official /models endpoint.
type RemoteModel struct {
	ID            string
	Name          string
	ContextWindow int
}

// FetchNativeProviderModels downloads the provider's official /models
// catalog. The endpoint exposes no per-model API-type information, so the
// curated NativeProviderSpec.Models list remains the source of truth for
// which models are usable by Codex's native Responses API.
func FetchNativeProviderModels(providerID string, timeout time.Duration) ([]RemoteModel, error) {
	spec, ok := NativeProvider(providerID)
	if !ok {
		return nil, fmt.Errorf("unsupported Codex native provider %q", providerID)
	}
	return fetchRemoteModels(strings.TrimSuffix(spec.BaseURL, "/")+"/models", timeout)
}

func fetchRemoteModels(endpoint string, timeout time.Duration) ([]RemoteModel, error) {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(endpoint)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: HTTP %d", endpoint, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", endpoint, err)
	}
	var parsed struct {
		Data []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Context int    `json:"context_length"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse %s: %w", endpoint, err)
	}
	models := make([]RemoteModel, 0, len(parsed.Data))
	for _, d := range parsed.Data {
		if strings.TrimSpace(d.ID) == "" {
			continue
		}
		name := d.Name
		if name == "" {
			name = d.ID
		}
		models = append(models, RemoteModel{ID: d.ID, Name: name, ContextWindow: d.Context})
	}
	return models, nil
}

// NativeProviderAPIKey resolves a native provider's API key from its
// environment variable first (including registered aliases, which lets
// OpenCode Zen and Go share one key), then from the same provider in
// proxy.toml. It returns the key and where it came from.
func NativeProviderAPIKey(providerID string) (string, string) {
	spec, ok := NativeProvider(providerID)
	if !ok {
		return "", ""
	}
	if key := strings.TrimSpace(os.Getenv(spec.EnvKey)); key != "" {
		return key, "$" + spec.EnvKey
	}
	for _, alias := range spec.EnvKeyAliases {
		if key := strings.TrimSpace(os.Getenv(alias)); key != "" {
			return key, "$" + alias
		}
	}
	if cfg, err := LoadProxyConfig(); err == nil {
		if provider := cfg.Providers[CodexProxyProviderID(providerID)]; provider != nil {
			if key := strings.TrimSpace(provider.AuthToken); key != "" {
				return key, "AIX provider configuration"
			}
		}
		if provider := cfg.Providers[providerID]; provider != nil {
			if key := strings.TrimSpace(provider.AuthToken); key != "" {
				return key, "AIX provider configuration"
			}
		}
		// DeepSeek uses a distinct Anthropic routing section for Claude, but
		// the credential is shared with its native Codex endpoint.
		if provider := cfg.Providers[ClaudeProxyProviderID(providerID)]; provider != nil {
			if key := strings.TrimSpace(provider.AuthToken); key != "" {
				return key, "AIX provider configuration"
			}
		}
	}
	return "", ""
}

// DeepSeekAPIKey is kept as a compatibility wrapper around the generic
// native provider key resolution.
func DeepSeekAPIKey() (string, string) {
	return NativeProviderAPIKey("deepseek")
}

// ResolveNativeModel defaults an empty model to the provider's default and
// validates the requested model against the provider's supported set.
func ResolveNativeModel(providerID, model string) (string, error) {
	spec, ok := NativeProvider(providerID)
	if !ok {
		return "", fmt.Errorf("unsupported Codex native provider %q", providerID)
	}
	if model == "" {
		model = spec.DefaultModel
	}
	if spec.AllowAnyModel {
		if strings.TrimSpace(model) == "" {
			return "", fmt.Errorf("a model is required for %s", spec.Name)
		}
		model = strings.TrimSpace(model)
		if canonical, ok := resolveAllowAnyModelAlias(spec, model); ok {
			return canonical, nil
		}
		if providerID == "openrouter" {
			if canonical, ok := openRouterModelAliases[model]; ok {
				return canonical, nil
			}
			if prefix := openRouterBlockedModel(model); prefix != "" {
				return "", fmt.Errorf("OpenRouter model %q is not allowed: %s models are curated out; use a DeepSeek slug", model, strings.TrimSuffix(prefix, "/"))
			}
			if !strings.Contains(model, "/") {
				return "", fmt.Errorf("OpenRouter model %q must be a vendor-prefixed ID (e.g. %q); bare aliases: %s",
					model, "deepseek/deepseek-v4-flash", sortedOpenRouterBareAliases())
			}
		}
		return model, nil
	}
	for _, m := range spec.Models {
		if strings.EqualFold(m.Slug, model) || strings.EqualFold(m.DisplayName, model) {
			return m.Slug, nil
		}
	}
	if normalized := slugifyModelName(model); normalized != "" {
		for _, m := range spec.Models {
			if m.Slug == normalized {
				return m.Slug, nil
			}
		}
	}
	return "", fmt.Errorf("unsupported %s Codex model %q (use %s)", spec.Name, model, strings.Join(NativeModels(providerID), " or "))
}

// resolveAllowAnyModelAlias maps human-readable model names to slugs for
// AllowAnyModel providers: curated display names match case-insensitively,
// and spaced names normalize to the lowercase hyphenated slug convention the
// gateways use (e.g. "Ox Alpha Free" -> "ox-alpha-free"). OpenRouter display
// names resolve to their vendor-prefixed IDs.
func resolveAllowAnyModelAlias(spec NativeProviderSpec, model string) (string, bool) {
	for _, m := range spec.Models {
		if strings.EqualFold(model, m.Slug) || strings.EqualFold(model, m.DisplayName) {
			return m.Slug, true
		}
	}
	switch spec.ID {
	case "opencode-zen", "opencode-go":
		for _, m := range openCodeZenModels {
			if strings.EqualFold(model, m.Slug) || strings.EqualFold(model, m.DisplayName) {
				return m.Slug, true
			}
		}
	case "openrouter":
		for _, m := range openRouterModels {
			if strings.EqualFold(model, m.DisplayName) {
				return m.Slug, true
			}
		}
	}
	if normalized := slugifyModelName(model); normalized != "" && normalized != model {
		return normalized, true
	}
	return "", false
}

// slugifyModelName converts a human-readable model name to the lowercase
// hyphenated slug convention used by the OpenCode gateways.
func slugifyModelName(model string) string {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(model)))
	return strings.Join(fields, "-")
}

// openRouterBlockedModel returns the matching prefix when slug names an OpenAI
// or Anthropic model. These are deliberately curated out of the OpenRouter
// provider; AllowAnyModel would otherwise accept any vendor/model slug, so the
// block keeps GPT/Claude slugs out of both switching and the Codex catalog.
func openRouterBlockedModel(slug string) string {
	lower := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(slug), "~"))
	for _, prefix := range []string{"openai/", "gpt-", "anthropic/", "claude-"} {
		if strings.HasPrefix(lower, prefix) {
			return prefix
		}
	}
	return ""
}

// sortedOpenRouterBareAliases returns the bare (unprefixed) model names that
// ResolveNativeModel normalizes for OpenRouter, sorted for stable messages.
func sortedOpenRouterBareAliases() string {
	bare := make([]string, 0, len(openRouterModelAliases))
	for name := range openRouterModelAliases {
		if !strings.Contains(name, "/") {
			bare = append(bare, name)
		}
	}
	sort.Strings(bare)
	return strings.Join(bare, ", ")
}

// ResolveCodexDeepSeekModel is kept as a compatibility wrapper.
func ResolveCodexDeepSeekModel(model string) (string, error) {
	return ResolveNativeModel("deepseek", model)
}

// CodexDeepSeekOptions identifies the files changed by the native DeepSeek
// integration. Explicit paths keep the write path testable without changing
// the process HOME. Kept for compatibility; prefer CodexNativeOptions.
type CodexDeepSeekOptions struct {
	APIKey           string
	Model            string
	ConfigPath       string
	ModelCatalogPath string
	BackupDir        string
}

// CodexNativeOptions identifies the files changed by a native provider
// integration. Explicit paths keep the write path testable without changing
// the process HOME.
type CodexNativeOptions struct {
	ProviderID       string
	APIKey           string
	BaseURL          string
	Model            string
	Effort           string
	ConfigPath       string
	ModelCatalogPath string
	BackupDir        string
}

// ConfigureCodexNative configures Codex to call a provider's native Responses
// API directly. AIX's local proxy is intentionally not involved.
func ConfigureCodexNative(providerID, model, apiKey string) error {
	return ConfigureCodexNativeWithEffort(providerID, model, "", apiKey)
}

// ConfigureCodexNativeWithEffort configures a native provider with an
// explicitly resolved reasoning effort. An empty effort uses the harness
// registry default.
func ConfigureCodexNativeWithEffort(providerID, model, effort, apiKey string) error {
	return ConfigureCodexNativeAt(CodexNativeOptions{
		ProviderID:       providerID,
		APIKey:           apiKey,
		Model:            model,
		Effort:           effort,
		ConfigPath:       CodexConfigPath(),
		ModelCatalogPath: CodexModelsPath(),
		BackupDir:        BackupsDir(),
	})
}

// ConfigureCodexProxyWithEffort configures Codex to use the local AIX gateway
// while retaining the provider's native Responses protocol end to end.
func ConfigureCodexProxyWithEffort(providerID, model, effort, upstreamKey string) error {
	baseURL, gatewayKey, err := EnsureCodexProxyProvider(providerID, upstreamKey)
	if err != nil {
		return err
	}
	return ConfigureCodexNativeAt(CodexNativeOptions{
		ProviderID:       providerID,
		APIKey:           gatewayKey,
		BaseURL:          baseURL,
		Model:            model,
		Effort:           effort,
		ConfigPath:       CodexConfigPath(),
		ModelCatalogPath: CodexModelsPath(),
		BackupDir:        BackupsDir(),
	})
}

func ConfigureCodexNativeAt(opts CodexNativeOptions) error {
	spec, ok := NativeProvider(opts.ProviderID)
	if !ok {
		return fmt.Errorf("unsupported Codex native provider %q", opts.ProviderID)
	}
	selection, err := ResolveHarnessSelection(HarnessCodex, opts.ProviderID, opts.Model, opts.Effort)
	if err != nil {
		return err
	}
	model := selection.ClientModel
	harness, ok := HarnessProvider(HarnessCodex, opts.ProviderID)
	if !ok {
		return fmt.Errorf("provider %q has no Codex harness mapping in %s", opts.ProviderID, HarnessRegistryPath())
	}
	if strings.TrimSpace(opts.APIKey) == "" {
		return fmt.Errorf("%s API key is required", spec.Name)
	}
	if opts.ConfigPath == "" || opts.ModelCatalogPath == "" || opts.BackupDir == "" {
		return errors.New("Codex configuration paths are required")
	}
	backupLabel := "codex-" + opts.ProviderID
	if err := backupTo(opts.ConfigPath, backupLabel, opts.BackupDir); err != nil {
		return fmt.Errorf("backup config: %w", err)
	}
	if err := backupTo(opts.ModelCatalogPath, backupLabel+"-models", opts.BackupDir); err != nil {
		return fmt.Errorf("backup model catalog: %w", err)
	}

	config, err := readTomlMap(opts.ConfigPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	if err := saveCodexNativeSnapshot(config, opts.ModelCatalogPath, opts.BackupDir); err != nil {
		return fmt.Errorf("save native Codex snapshot: %w", err)
	}
	providers, _ := config["model_providers"].(map[string]interface{})
	if providers == nil {
		providers = make(map[string]interface{})
	}
	baseURL := harness.BaseURL
	if strings.TrimSpace(opts.BaseURL) != "" {
		baseURL = strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	}
	providers[opts.ProviderID] = map[string]interface{}{
		"name":                      spec.Name,
		"base_url":                  baseURL,
		"wire_api":                  "responses",
		"experimental_bearer_token": strings.TrimSpace(opts.APIKey),
	}
	config["model"] = model
	config["model_provider"] = opts.ProviderID
	config["preferred_auth_method"] = "apikey"
	config["forced_login_method"] = "api"
	config["model_reasoning_effort"] = selection.Effort
	config["model_catalog_json"] = opts.ModelCatalogPath
	config["model_providers"] = providers
	if err := writeTomlPrivate(opts.ConfigPath, config); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if err := writeNativeModelCatalog(opts.ProviderID, opts.ModelCatalogPath, model); err != nil {
		return fmt.Errorf("write model catalog: %w", err)
	}
	return nil
}

// ConfigureCodexDeepSeek is kept as a compatibility wrapper.
func ConfigureCodexDeepSeek(model, apiKey string) error {
	return ConfigureCodexNative("deepseek", model, apiKey)
}

// ConfigureCodexDeepSeekAt is kept as a compatibility wrapper.
func ConfigureCodexDeepSeekAt(opts CodexDeepSeekOptions) error {
	return ConfigureCodexNativeAt(CodexNativeOptions{
		ProviderID:       "deepseek",
		APIKey:           opts.APIKey,
		Model:            opts.Model,
		ConfigPath:       opts.ConfigPath,
		ModelCatalogPath: opts.ModelCatalogPath,
		BackupDir:        opts.BackupDir,
	})
}

// RemoveCodexNativeModels removes a native provider's catalog entries without
// touching any other user-provided model metadata.
func RemoveCodexNativeModels(providerID, path string) error {
	spec, ok := NativeProvider(providerID)
	if !ok {
		return nil
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var catalog map[string]json.RawMessage
	if err := json.Unmarshal(raw, &catalog); err != nil {
		return fmt.Errorf("parse model catalog: %w", err)
	}
	var models []json.RawMessage
	if rawModels, ok := catalog["models"]; ok && len(rawModels) > 0 {
		if err := json.Unmarshal(rawModels, &models); err != nil {
			return fmt.Errorf("parse model entries: %w", err)
		}
	}
	filtered, err := withoutNativeModels(spec, models)
	if err != nil {
		return err
	}
	if len(filtered) == 0 && len(catalog) == 1 {
		return os.Remove(path)
	}
	encodedModels, err := json.Marshal(filtered)
	if err != nil {
		return err
	}
	catalog["models"] = encodedModels
	out, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateFile(path, append(out, '\n'))
}

// RemoveCodexDeepSeekModels is kept as a compatibility wrapper.
func RemoveCodexDeepSeekModels(path string) error {
	return RemoveCodexNativeModels("deepseek", path)
}

// RemoveAllCodexNativeModels removes catalog entries for every registered
// native provider, used when restoring Codex to its default native API.
func RemoveAllCodexNativeModels(path string) error {
	for id := range NativeProviderSpecs() {
		if err := RemoveCodexNativeModels(id, path); err != nil {
			return err
		}
	}
	return nil
}

// CodexLoginOptions identifies the config file changed by the login label
// setter. Explicit paths keep the write path testable.
type CodexLoginOptions struct {
	Login      string
	ConfigPath string
}

// SetCodexProviderLogin sets the display name (login label) of the active
// custom provider in ~/.codex/config.toml. It only applies when Codex uses a
// custom model provider; the default GPT mode has no renameable label.
func SetCodexProviderLogin(login string) error {
	return SetCodexProviderLoginAt(CodexLoginOptions{
		Login:      login,
		ConfigPath: CodexConfigPath(),
	})
}

func SetCodexProviderLoginAt(opts CodexLoginOptions) error {
	if opts.ConfigPath == "" {
		return errors.New("Codex configuration path is required")
	}
	config, err := readTomlMap(opts.ConfigPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", opts.ConfigPath, err)
	}
	providerID, _ := config["model_provider"].(string)
	providers, _ := config["model_providers"].(map[string]interface{})
	if providerID == "" || providers == nil {
		return errors.New("Codex is in default GPT mode; the login label cannot be changed")
	}
	p, ok := providers[providerID].(map[string]interface{})
	if !ok {
		return errors.New("Codex is in default GPT mode; the login label cannot be changed")
	}
	if opts.Login == "" {
		delete(p, "name")
	} else {
		p["name"] = opts.Login
	}
	config["model_providers"] = providers
	if err := writeTomlPrivate(opts.ConfigPath, config); err != nil {
		return fmt.Errorf("write %s: %w", opts.ConfigPath, err)
	}
	return nil
}

func readTomlMap(path string) (map[string]interface{}, error) {
	config := make(map[string]interface{})
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return config, nil
	}
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return config, nil
	}
	if _, err := toml.Decode(string(raw), &config); err != nil {
		return nil, err
	}
	return config, nil
}

func writeTomlPrivate(path string, config map[string]interface{}) error {
	var out bytes.Buffer
	if err := toml.NewEncoder(&out).Encode(config); err != nil {
		return err
	}
	return writePrivateFile(path, out.Bytes())
}

func writePrivateFile(path string, contents []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".aix-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(contents); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	return os.Chmod(path, 0600)
}

func backupTo(path, label, dir string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	name := fmt.Sprintf("%s.%s.%s.bak", filepath.Base(path), label, time.Now().Format("20060102-150405.000"))
	if err := writePrivateFile(filepath.Join(dir, name), data); err != nil {
		return err
	}
	return nil
}

// writeNativeModelCatalog rewrites the models list in ~/.codex/models.json so
// it contains exactly the active provider's catalog entries. Entries left
// behind by a previously active provider are dropped; otherwise they would
// stay selectable in the Codex model picker even though the active provider
// cannot serve them. Top-level catalog keys outside "models" are preserved.
// DeepSeek metadata is sourced from the official Codex catalog (the embedded
// snapshot, or a live copy cached by RefreshDeepSeekCatalog) instead of a
// hand-written factory, so model metadata adapts automatically. For
// AllowAnyModel providers (e.g. OpenRouter) the active model is also written
// when it is not part of the curated list, so --model overrides get a usable
// catalog entry.
func writeNativeModelCatalog(providerID, path, activeModel string) error {
	spec, ok := NativeProvider(providerID)
	if !ok {
		return fmt.Errorf("unsupported Codex native provider %q", providerID)
	}
	catalog := map[string]json.RawMessage{}
	if raw, err := os.ReadFile(path); err == nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, &catalog); err != nil {
			return fmt.Errorf("parse existing catalog: %w", err)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}

	var official map[string]map[string]interface{}
	if providerID == "deepseek" {
		official = deepSeekCatalogSource()
	}
	harness, ok := HarnessProvider(HarnessCodex, providerID)
	if !ok {
		return fmt.Errorf("provider %q has no Codex harness mapping in %s", providerID, HarnessRegistryPath())
	}
	var models []json.RawMessage
	modelIDs := sortedHarnessModelIDs(harness.Models)
	for _, modelID := range modelIDs {
		mapped := harness.Models[modelID]
		slug := mapped.ClientModel
		metadata := minimalNativeCatalogMetadata(slug)
		if official != nil {
			if entry, ok := official[slug]; ok {
				metadata = entry
			} else {
				contextWindow := mapped.ContextWindow
				if contextWindow == 0 {
					contextWindow = 400000
				}
				metadata = codexCompatibleCatalogMetadata(slug, mapped.DisplayName, "", codexBaseInstructions, 0, contextWindow)
			}
		} else if spec.CatalogMetadata != nil {
			metadata = spec.CatalogMetadata(slug)
		}
		applyHarnessEffortMetadata(metadata, mapped.SupportedEfforts, mapped.DefaultEffort)
		raw, err := json.Marshal(metadata)
		if err != nil {
			return err
		}
		models = append(models, raw)
	}
	activeMapped := false
	for _, mapped := range harness.Models {
		if mapped.ClientModel == activeModel {
			activeMapped = true
			break
		}
	}
	if spec.AllowAnyModel && activeModel != "" && !activeMapped && (providerID != "openrouter" || openRouterBlockedModel(activeModel) == "") {
		metadata := minimalNativeCatalogMetadata(activeModel)
		if spec.CatalogMetadata != nil {
			metadata = spec.CatalogMetadata(activeModel)
		}
		raw, err := json.Marshal(metadata)
		if err != nil {
			return err
		}
		models = append(models, raw)
	}
	encodedModels, err := json.Marshal(models)
	if err != nil {
		return err
	}
	catalog["models"] = encodedModels
	out, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateFile(path, append(out, '\n'))
}

func applyHarnessEffortMetadata(metadata map[string]interface{}, efforts []string, defaultEffort string) {
	levels := make([]map[string]string, 0, len(efforts))
	for _, effort := range efforts {
		description := "Reasoning effort: " + effort
		switch effort {
		case "low":
			description = "Fast responses with lighter reasoning"
		case "medium":
			description = "Balanced reasoning depth and latency"
		case "high":
			description = "Extra reasoning depth for complex problems"
		case "xhigh":
			description = "Very high reasoning depth"
		case "max":
			description = "Maximum reasoning depth for the hardest problems"
		}
		levels = append(levels, map[string]string{"effort": effort, "description": description})
	}
	metadata["supported_reasoning_levels"] = levels
	if !containsString(efforts, defaultEffort) && len(efforts) > 0 {
		defaultEffort = efforts[0]
	}
	metadata["default_reasoning_level"] = defaultEffort
}

// SyncLiveModelCatalog fetches the provider's live /models catalog and
// rewrites ~/.codex/models.json with only models verified for native Responses
// mode. Unknown models are deliberately excluded because the endpoint does not
// expose reliable per-model multi-turn/tool-call compatibility.
func SyncLiveModelCatalog(providerID string) (int, error) {
	spec, ok := NativeProvider(providerID)
	if !ok {
		return 0, fmt.Errorf("unsupported Codex native provider %q", providerID)
	}
	remote, err := FetchNativeProviderModels(providerID, 15*time.Second)
	if err != nil {
		return 0, fmt.Errorf("fetch live catalog: %w", err)
	}
	config, err := readTomlMap(CodexConfigPath())
	if err != nil {
		return 0, err
	}
	activeModel, _ := config["model"].(string)
	return syncLiveModelCatalog(spec, CodexModelsPath(), activeModel, remote)
}

// syncLiveModelCatalog filters a fetched catalog to the Codex-verified model
// list and writes it. The active model is only preserved when it is itself
// verified; otherwise an unverified active model would be advertised even
// though the provider config still points at it.
func syncLiveModelCatalog(spec NativeProviderSpec, path, activeModel string, remote []RemoteModel) (int, error) {
	compatible := make([]RemoteModel, 0, len(remote))
	for _, model := range remote {
		if IsNativeModel(spec.ID, model.ID) {
			compatible = append(compatible, model)
		}
	}
	if activeModel != "" && IsNativeModel(spec.ID, activeModel) && !containsRemoteModel(compatible, activeModel) {
		compatible = append(compatible, RemoteModel{ID: activeModel})
	}
	if len(compatible) == 0 {
		return 0, fmt.Errorf("live catalog for %q contains no models verified for Codex Responses API", spec.ID)
	}
	if err := writeLiveModelCatalog(spec, path, activeModel, compatible); err != nil {
		return 0, err
	}
	return len(compatible), nil
}

// writeLiveModelCatalog writes every live remote model into the Codex catalog
// file, preserving top-level keys outside "models". Curated metadata (display
// name, description, priority, context window) is reused when the provider's
// model list or the shared OpenCode Zen list knows the slug; unknown slugs
// fall back to the raw ID so the entry stays usable. The active model is
// appended only when it is in the verified list and the live list omits it.
func writeLiveModelCatalog(spec NativeProviderSpec, path, activeModel string, remote []RemoteModel) error {
	catalog := map[string]json.RawMessage{}
	if raw, err := os.ReadFile(path); err == nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, &catalog); err != nil {
			return fmt.Errorf("parse existing catalog: %w", err)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	known := knownNativeModelMetadata(spec)
	type catalogModel struct {
		slug     string
		metadata map[string]interface{}
	}
	entries := make([]catalogModel, 0, len(remote)+1)
	for _, m := range remote {
		entries = append(entries, catalogModel{slug: m.ID, metadata: liveCatalogMetadata(known, m)})
	}
	if activeModel != "" && !containsRemoteModel(remote, activeModel) && nativeModelInList(spec, activeModel) {
		entries = append(entries, catalogModel{slug: activeModel, metadata: liveCatalogMetadata(known, RemoteModel{ID: activeModel})})
	}
	// Deterministic output: curated priority first (unknown slugs sort last),
	// then slug for stable diffs.
	sort.SliceStable(entries, func(i, j int) bool {
		pi, pj := entries[i].metadata["priority"].(int), entries[j].metadata["priority"].(int)
		if pi == 0 {
			pi = 1 << 30
		}
		if pj == 0 {
			pj = 1 << 30
		}
		if pi != pj {
			return pi < pj
		}
		return entries[i].slug < entries[j].slug
	})
	models := make([]json.RawMessage, 0, len(entries))
	for _, e := range entries {
		raw, err := json.Marshal(e.metadata)
		if err != nil {
			return err
		}
		models = append(models, raw)
	}
	encodedModels, err := json.Marshal(models)
	if err != nil {
		return err
	}
	catalog["models"] = encodedModels
	out, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateFile(path, append(out, '\n'))
}

// knownNativeModelMetadata merges the provider's curated model list with the
// shared OpenCode Zen list so live catalog slugs reuse display names and
// context windows instead of degrading to raw IDs.
func knownNativeModelMetadata(spec NativeProviderSpec) map[string]NativeModelSpec {
	known := make(map[string]NativeModelSpec, len(spec.Models)+len(openCodeZenModels))
	for _, m := range spec.Models {
		known[m.Slug] = m
	}
	for _, m := range openCodeZenModels {
		if _, ok := known[m.Slug]; !ok {
			known[m.Slug] = m
		}
	}
	return known
}

// liveCatalogMetadata renders a full Codex catalog entry for one live remote
// model, reusing curated metadata when the slug is known and falling back to
// the raw ID for display name when it is not.
func liveCatalogMetadata(known map[string]NativeModelSpec, m RemoteModel) map[string]interface{} {
	curated := NativeModelSpec{Slug: m.ID}
	if s, ok := known[m.ID]; ok {
		curated = s
	}
	contextWindow := 400000
	if curated.ContextWindow > 0 {
		contextWindow = curated.ContextWindow
	} else if m.ContextWindow > 0 {
		contextWindow = m.ContextWindow
	}
	displayName := curated.DisplayName
	if displayName == "" {
		displayName = m.ID
	}
	return codexCompatibleCatalogMetadata(m.ID, displayName, curated.Description, codexBaseInstructions, curated.Priority, contextWindow)
}

// containsRemoteModel reports whether a live model ID is present in the
// fetched remote catalog.
func containsRemoteModel(remote []RemoteModel, id string) bool {
	for _, m := range remote {
		if m.ID == id {
			return true
		}
	}
	return false
}

// deepSeekCatalogCache holds a live-fetched official DeepSeek catalog used by
// writeNativeModelCatalog after `aix codex deepseek` refreshed it. A nil map
// falls back to AIX's bundled metadata.
var deepSeekCatalogCache map[string]map[string]interface{}

// RefreshDeepSeekCatalog best-effort refreshes the DeepSeek entries in
// ~/.codex/models.json from DeepSeek's official Codex setup script, so model
// metadata adapts on the next switch without an aix release. Any failure
// (offline, malformed payload, Codex not on DeepSeek) is ignored; the switch
// already wrote AIX's bundled fallback entries.
func RefreshDeepSeekCatalog() {
	catalog, err := FetchOfficialDeepSeekCatalogQuick()
	if err != nil {
		return
	}
	deepSeekCatalogCache = catalog
	config, err := readTomlMap(CodexConfigPath())
	if err != nil {
		return
	}
	if providerID, _ := config["model_provider"].(string); providerID != "deepseek" {
		return
	}
	model, _ := config["model"].(string)
	_ = writeNativeModelCatalog("deepseek", CodexModelsPath(), model)
}

// deepSeekCatalogSource returns the DeepSeek catalog to write: a live-refreshed
// copy when one is cached, otherwise AIX's generated fallback metadata.
func deepSeekCatalogSource() map[string]map[string]interface{} {
	if len(deepSeekCatalogCache) > 0 {
		return deepSeekCatalogCache
	}
	bundled, _ := BundledDeepSeekCatalog()
	return bundled
}

// writeDeepSeekModelCatalog is kept as a compatibility wrapper.
func writeDeepSeekModelCatalog(path string) error {
	return writeNativeModelCatalog("deepseek", path, "")
}

func nativeModelInList(spec NativeProviderSpec, model string) bool {
	for _, m := range spec.Models {
		if m.Slug == model {
			return true
		}
	}
	return false
}

func withoutNativeModels(spec NativeProviderSpec, models []json.RawMessage) ([]json.RawMessage, error) {
	owned := make(map[string]bool, len(spec.Models))
	for _, m := range spec.Models {
		owned[m.Slug] = true
	}
	filtered := make([]json.RawMessage, 0, len(models))
	for _, raw := range models {
		var model struct {
			Slug string `json:"slug"`
		}
		if err := json.Unmarshal(raw, &model); err != nil {
			return nil, fmt.Errorf("parse model entry: %w", err)
		}
		if !owned[model.Slug] {
			filtered = append(filtered, raw)
		}
	}
	return filtered, nil
}

// withoutDeepSeekModels is kept as a compatibility wrapper.
func withoutDeepSeekModels(models []json.RawMessage) ([]json.RawMessage, error) {
	spec, _ := NativeProvider("deepseek")
	return withoutNativeModels(spec, models)
}

func isDeepSeekV4Model(model string) bool {
	for _, m := range NativeModels("deepseek") {
		if m == model {
			return true
		}
	}
	return false
}

// modelCatalogMetadata renders the catalog entry for a model of a native
// provider, looking up display name/description/priority from the provider's
// model list (unknown slugs fall back to the slug itself, so AllowAnyModel
// providers can still get a usable entry). Codex 0.147+ makes
// `base_instructions` a required field, so the entry embeds AIX's own
// system-prompt template (assets/codex_base_instructions.txt). Capability
// fields follow the interoperable shape DeepSeek's setup script publishes; the
// context window is the model's ContextWindow when the curated spec declares
// one, otherwise a provider-level approximation.
func modelCatalogMetadata(models []NativeModelSpec, baseInstructions string, contextWindow int, model string) map[string]interface{} {
	displayName := model
	description := ""
	priority := 0
	for _, m := range models {
		if m.Slug == model {
			displayName = m.DisplayName
			description = m.Description
			priority = m.Priority
			if m.ContextWindow > 0 {
				contextWindow = m.ContextWindow
			}
			break
		}
	}
	metadata := codexCompatibleCatalogMetadata(model, displayName, description, baseInstructions, priority, contextWindow)
	if strings.Contains(strings.ToLower(model), "vision-exp") {
		metadata["input_modalities"] = []string{"text", "image"}
		metadata["supports_image_detail_original"] = true
	}
	return metadata
}

// codexCompatibleCatalogMetadata is the shared capability map for native
// providers. The payload is identical across entries except for slug,
// display_name, description, and priority.
func codexCompatibleCatalogMetadata(model, displayName, description, baseInstructions string, priority, contextWindow int) map[string]interface{} {
	return map[string]interface{}{
		"slug":                           model,
		"prefer_websockets":              false,
		"support_verbosity":              true,
		"default_verbosity":              "low",
		"apply_patch_tool_type":          "freeform",
		"web_search_tool_type":           "text",
		"input_modalities":               []string{"text"},
		"supports_image_detail_original": false,
		"truncation_policy": map[string]interface{}{
			"mode":  "tokens",
			"limit": 10000,
		},
		"supports_parallel_tool_calls":      true,
		"tool_mode":                         nil,
		"multi_agent_version":               "v2",
		"use_responses_lite":                false,
		"include_skills_usage_instructions": false,
		"auto_review_model_override":        nil,
		"context_window":                    contextWindow,
		"max_context_window":                contextWindow,
		"effective_context_window_percent":  95,
		"auto_compact_token_limit":          nil,
		"comp_hash":                         "3000",
		"reasoning_summary_format":          "experimental",
		"default_reasoning_summary":         "none",
		"display_name":                      displayName,
		"description":                       description,
		"default_reasoning_level":           "high",
		"supported_reasoning_levels":        nativeReasoningLevels,
		"shell_type":                        "shell_command",
		"visibility":                        "list",
		"minimal_client_version":            "0.144.0",
		"supported_in_api":                  true,
		"availability_nux":                  nil,
		"upgrade":                           nil,
		"priority":                          priority,
		"model_messages": map[string]interface{}{
			"instructions_template": baseInstructions,
			"instructions_variables": map[string]string{
				"personality_default":   "",
				"personality_friendly":  "",
				"personality_pragmatic": "",
			},
			"approvals": nil,
		},
		"experimental_supported_tools": []interface{}{},
		"supports_search_tool":         true,
		"default_service_tier":         nil,
		"supports_reasoning_summaries": true,
		"base_instructions":            baseInstructions,
	}
}

// nativeReasoningLevels is the reasoning-effort shape the Codex client
// surfaces in its effort picker. Codex 0.148+ desktop builds read
// `supported_reasoning_levels` and `default_reasoning_level` from the
// `model_catalog_json` file through the app-server `models list` API, so
// every catalog entry we write needs them for the effort selector to appear.
var nativeReasoningLevels = []map[string]string{
	{"effort": "low", "description": "Fast responses with lighter reasoning"},
	{"effort": "high", "description": "Extra high reasoning depth for complex problems"},
	{"effort": "max", "description": "Maximum reasoning depth for the hardest problems"},
}

// minimalNativeCatalogMetadata is the fallback catalog entry for native
// providers that do not supply a rich metadata factory (user-defined native
// providers from ~/.aix/native.toml and arbitrary AllowAnyModel overrides).
// Besides the identity fields it carries the reasoning-effort and visibility
// fields so Codex 0.148+ desktop surfaces the model name and an effort picker
// just like the curated providers; the default matches the
// The effective harness registry supplies the selected model's supported and
// default reasoning effort metadata.
func minimalNativeCatalogMetadata(model string) map[string]interface{} {
	return map[string]interface{}{
		"slug":                       model,
		"display_name":               model,
		"description":                "",
		"priority":                   0,
		"visibility":                 "list",
		"default_reasoning_level":    "high",
		"supported_reasoning_levels": nativeReasoningLevels,
	}
}

// deepSeekV4Metadata supplies interoperable per-model metadata for DeepSeek's
// Codex integration. Capability fields match the published behavior of
// deepseek-v4-flash, deepseek-v4-pro, and the experimental vision model.
func deepSeekV4Metadata(model, baseInstructions string) map[string]interface{} {
	displayName := "DeepSeek-V4-Flash"
	description := "Latest frontier agentic coding model."
	priority := 1
	switch model {
	case DeepSeekV4ProModel:
		displayName = "DeepSeek-V4-Pro"
		description = "Most capable frontier agentic coding model."
		priority = 2
	case DeepSeekV4VisionModel:
		displayName = "DeepSeek-V4-Flash-Vision"
		description = "Latest frontier agentic coding model with image input."
		priority = 3
	}
	metadata := codexCompatibleCatalogMetadata(model, displayName, description, baseInstructions, priority, deepSeekV4ContextWindow)
	if model == DeepSeekV4VisionModel {
		metadata["input_modalities"] = []string{"text", "image"}
		metadata["supports_image_detail_original"] = true
	}
	return metadata
}
