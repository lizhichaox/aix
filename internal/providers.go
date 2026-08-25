package internal

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	// DeepSeekAnthropicProviderID is the proxy provider that speaks the
	// Anthropic Messages API for DeepSeek (Claude Code and Claude Desktop).
	DeepSeekAnthropicProviderID = "deepseek-anthropic"

	// ClaudeCodeDeepSeekModel is the Anthropic-shaped model name Claude Code
	// sends for DeepSeek V4 Flash. It is presented as Claude Opus 5 and the
	// proxy always routes it to deepseek-v4-flash.
	ClaudeCodeDeepSeekModel = "claude-opus-5"

	// ClaudeCodeDeepSeekModelName is the Claude-facing display name for the
	// DeepSeek V4 Flash alias.
	ClaudeCodeDeepSeekModelName = "Claude Opus 5"

	// ClaudeCodeDeepSeekProModel is the Anthropic-shaped model name Claude
	// Code sends for DeepSeek V4 Pro. It is presented as Claude Fable 5 and
	// the proxy always routes it to deepseek-v4-pro.
	ClaudeCodeDeepSeekProModel = "claude-fable-5"

	// ClaudeCodeDeepSeekProModelName is the Claude-facing display name for
	// the DeepSeek V4 Pro alias.
	ClaudeCodeDeepSeekProModelName = "Claude Fable 5"

	// DefaultClaudeUpstreamModel is the canonical DeepSeek model selected when
	// a Claude provider is specified without an explicit model. Providers whose
	// catalogs use vendor-qualified slugs add the deepseek/ prefix at the
	// registry boundary.
	DefaultClaudeUpstreamModel = "deepseek-v4-flash-vision-exp"
	DefaultHarnessEffort       = "high"
	DefaultClaudeEffort        = DefaultHarnessEffort // compatibility name
)

// DeepSeekUpstreamModels are the DeepSeek upstream model names aix knows.
var DeepSeekUpstreamModels = []string{DefaultClaudeUpstreamModel, DeepSeekV4FlashModel, DeepSeekV4ProModel}

// ClaudeDesktopDeepSeekModels are the Claude Desktop model names shown in the
// picker. Each maps to exactly one DeepSeek upstream model: claude-opus-5 is
// always deepseek-v4-flash and claude-fable-5 is always deepseek-v4-pro.
var ClaudeDesktopDeepSeekModels = []string{
	defaultClaudeAlias(),
	ClaudeCodeDeepSeekModel,
	ClaudeCodeDeepSeekProModel,
}

// ValidDeepSeekUpstreamModel reports whether model is a syntactically valid
// DeepSeek upstream model id. The API adds models independently of AIX
// releases, so switching must not be limited to the embedded curated list.
func ValidDeepSeekUpstreamModel(model string) bool {
	if !strings.HasPrefix(model, "deepseek-") || len(model) > 200 {
		return false
	}
	for _, r := range model {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

// ClaudeDeepSeekAlias returns the Claude-facing model alias and display name
// for a DeepSeek upstream model: V4 Flash is Claude Opus 5 and V4 Pro is
// Claude Fable 5.
func ClaudeDeepSeekAlias(model string) (alias, displayName string) {
	switch model {
	case DeepSeekV4FlashModel:
		return ClaudeCodeDeepSeekModel, ClaudeCodeDeepSeekModelName
	case DeepSeekV4ProModel:
		return ClaudeCodeDeepSeekProModel, ClaudeCodeDeepSeekProModelName
	}
	if !ValidDeepSeekUpstreamModel(model) {
		return "", ""
	}
	// Claude Desktop accepts Anthropic-shaped ids, not raw deepseek-* ids.
	// A stable digest avoids alias collisions while remaining deterministic
	// across provider switches and AIX upgrades.
	digest := sha256.Sum256([]byte(model))
	// Claude Desktop recognizes fable-* as an effort-capable model family.
	// Keeping the dynamic id in that family exposes its effort selector while
	// the digest still gives every upstream model a stable, unique alias.
	return fmt.Sprintf("claude-fable-aix-%x", digest[:8]), model
}

// ClaudeDeepSeekAliasName returns the Claude-facing display name for an
// Anthropic-shaped alias, falling back to the alias itself.
func ClaudeDeepSeekAliasName(alias string) string {
	switch alias {
	case ClaudeCodeDeepSeekModel:
		return ClaudeCodeDeepSeekModelName
	case ClaudeCodeDeepSeekProModel:
		return ClaudeCodeDeepSeekProModelName
	}
	return alias
}

// DeepSeekModelDisplayName returns the Claude-facing name for a DeepSeek
// model, used by Claude Code settings and the CLI help.
func DeepSeekModelDisplayName(model string) string {
	if _, name := ClaudeDeepSeekAlias(model); name != "" {
		return name
	}
	return model
}

// ClaudeModel pairs a Claude-facing model alias with its display name and the
// upstream model the proxy rewrites it to. The alias must stay Anthropic-shaped
// (e.g. claude-opus-5) because Claude Desktop's model guard rejects ids like
// deepseek-* or vendor-prefixed slugs; the proxy performs the rewrite so the
// upstream never sees the alias.
//
// ContextWindow is the upstream model's native context window in tokens. It is
// zero when unknown; Claude Desktop only offers a 1M-context variant
// (supports1m/prefer1m in the config library entry) for models whose window is
// known to be at least 1M tokens, so the picker never promises more than the
// upstream can deliver.
type ClaudeModel struct {
	Alias         string
	DisplayName   string
	Upstream      string
	ContextWindow int
}

// ClaudeProviderSpec is the single source of truth for a Claude provider's
// curated models and its defaults when no model is supplied on the CLI.
type ClaudeProviderSpec struct {
	DefaultModel  string
	DefaultEffort string
	Models        []ClaudeModel
}

// oneMillionContext is the minimum context window (in tokens) for a model to
// be offered as a 1M-context variant in Claude Desktop.
const oneMillionContext = 1_000_000

// Supports1M reports whether the model's context window is known to be at
// least 1M tokens.
func (m ClaudeModel) Supports1M() bool {
	return m.ContextWindow >= oneMillionContext
}

// ClaudeCodeModelID returns the model id Claude Code should receive. Claude
// Code uses the [1m] suffix as the client-side signal for its extended context
// mode; the AIX proxy strips the suffix before forwarding the upstream model.
func ClaudeCodeModelID(m ClaudeModel) string {
	if m.Supports1M() {
		return m.Alias + "[1m]"
	}
	return m.Alias
}

// ClaudeDesktop1MModelID returns the legacy explicit 1M gateway alias. New
// config entries use Claude Desktop's native supports1m/prefer1m fields, but
// the proxy retains this mapping so conversations created by older AIX
// versions continue to work.
func ClaudeDesktop1MModelID(alias string) string {
	return alias + "-1m"
}

var deepseekClaudeModels = []ClaudeModel{
	{Alias: defaultClaudeAlias(), DisplayName: DefaultClaudeUpstreamModel, Upstream: DefaultClaudeUpstreamModel, ContextWindow: deepSeekV4ContextWindow},
	{Alias: ClaudeCodeDeepSeekModel, DisplayName: ClaudeCodeDeepSeekModelName, Upstream: DeepSeekV4FlashModel, ContextWindow: 1048576},
	{Alias: ClaudeCodeDeepSeekProModel, DisplayName: ClaudeCodeDeepSeekProModelName, Upstream: DeepSeekV4ProModel, ContextWindow: 1048576},
}

func defaultClaudeAlias() string {
	alias, _ := ClaudeDeepSeekAlias(DefaultClaudeUpstreamModel)
	return alias
}

// opencodeZenClaudeModels are the Claude models OpenCode Zen serves through
// its native Anthropic Messages endpoint (https://opencode.ai/zen/v1/messages).
// Most upstream ids are already Anthropic-shaped; non-Claude defaults receive
// a stable Claude-shaped alias for Claude Desktop compatibility.
var opencodeZenClaudeModels = []ClaudeModel{
	{Alias: defaultClaudeAlias(), DisplayName: DefaultClaudeUpstreamModel, Upstream: DefaultClaudeUpstreamModel},
	{Alias: "claude-sonnet-5", DisplayName: "Claude Sonnet 5", Upstream: "claude-sonnet-5"},
	{Alias: "claude-opus-5", DisplayName: "Claude Opus 5", Upstream: "claude-opus-5"},
	{Alias: "claude-fable-5", DisplayName: "Claude Fable 5", Upstream: "claude-fable-5"},
	{Alias: "claude-sonnet-4-6", DisplayName: "Claude Sonnet 4.6", Upstream: "claude-sonnet-4-6"},
	{Alias: "claude-haiku-4-5", DisplayName: "Claude Haiku 4.5", Upstream: "claude-haiku-4-5"},
	{Alias: "claude-opus-4-8", DisplayName: "Claude Opus 4.8", Upstream: "claude-opus-4-8"},
}

// opencodeGoClaudeModels are the OpenCode Go models served through its
// Anthropic Messages endpoint (https://opencode.ai/zen/go/v1/messages): the
// MiniMax, Qwen3.x, and DeepSeek V4. The upstream ids are
// not Anthropic-shaped, so Claude-shaped aliases are mapped through the proxy.
var opencodeGoClaudeModels = []ClaudeModel{
	{Alias: defaultClaudeAlias(), DisplayName: DefaultClaudeUpstreamModel, Upstream: DefaultClaudeUpstreamModel, ContextWindow: deepSeekV4ContextWindow},
	{Alias: "claude-opus-aix-opencode-go-flash", DisplayName: "DeepSeek V4 Flash", Upstream: DeepSeekV4FlashModel, ContextWindow: deepSeekV4ContextWindow},
	{Alias: "claude-fable-aix-opencode-go-pro", DisplayName: "DeepSeek V4 Pro", Upstream: DeepSeekV4ProModel, ContextWindow: deepSeekV4ContextWindow},
	{Alias: "claude-sonnet-5", DisplayName: "Claude Sonnet 5", Upstream: "minimax-m3"},
	{Alias: "claude-sonnet-4-6", DisplayName: "Claude Sonnet 4.6", Upstream: "minimax-m2.7"},
	{Alias: "claude-haiku-4-5", DisplayName: "Claude Haiku 4.5", Upstream: "minimax-m2.5"},
	{Alias: "claude-fable-5", DisplayName: "Claude Fable 5", Upstream: "qwen3.7-max"},
	{Alias: "claude-sonnet-4-5", DisplayName: "Claude Sonnet 4.5", Upstream: "qwen3.8-max"},
	{Alias: "claude-opus-4-5", DisplayName: "Claude Opus 4.5", Upstream: "qwen3.7-plus"},
	{Alias: "claude-opus-4-6", DisplayName: "Claude Opus 4.6", Upstream: "qwen3.6-plus"},
}

// openRouterClaudeModels are the curated OpenRouter Anthropic models that
// speak the Anthropic Messages API (OpenRouter's "Anthropic skin"). Aliases
// are Anthropic-shaped for the desktop model guard; the proxy rewrites them
// to OpenRouter's vendor-prefixed slugs. Note that OpenRouter geo-restricts
// anthropic/* models for accounts in some regions, while the DeepSeek models
// below remain available.
var openRouterClaudeModels = []ClaudeModel{
	{Alias: "claude-sonnet-4-6", DisplayName: "Claude Sonnet 4.6", Upstream: "anthropic/claude-sonnet-4.6"},
	{Alias: "claude-opus-5", DisplayName: "Claude Opus 5", Upstream: "anthropic/claude-opus-5"},
	{Alias: "claude-opus-4-8", DisplayName: "Claude Opus 4.8", Upstream: "anthropic/claude-opus-4.8"},
	{Alias: "claude-fable-5", DisplayName: "Claude Fable 5", Upstream: "anthropic/claude-fable-5"},
}

// openRouterDeepSeekModels are the DeepSeek models OpenRouter serves through
// its Anthropic Messages endpoint, mirroring the official /api/v1/models
// catalog. They use the raw vendor/model slugs as identity aliases: Claude
// Code accepts them directly, while the Claude Desktop model guard only
// accepts the Anthropic-shaped curated aliases above (raw slugs are filtered
// out of the desktop picker).
var openRouterDeepSeekModels = []ClaudeModel{
	{Alias: defaultClaudeAlias(), DisplayName: "DeepSeek V4 Flash Vision Exp", Upstream: "deepseek/" + DefaultClaudeUpstreamModel},
	{Alias: "deepseek/deepseek-chat", DisplayName: "DeepSeek V3", Upstream: "deepseek/deepseek-chat"},
	{Alias: "deepseek/deepseek-chat-v3-0324", DisplayName: "DeepSeek V3 0324", Upstream: "deepseek/deepseek-chat-v3-0324"},
	{Alias: "deepseek/deepseek-chat-v3.1", DisplayName: "DeepSeek V3.1", Upstream: "deepseek/deepseek-chat-v3.1"},
	{Alias: "deepseek/deepseek-r1", DisplayName: "DeepSeek R1", Upstream: "deepseek/deepseek-r1"},
	{Alias: "deepseek/deepseek-r1-0528", DisplayName: "DeepSeek R1 0528", Upstream: "deepseek/deepseek-r1-0528"},
	{Alias: "deepseek/deepseek-r1-distill-llama-70b", DisplayName: "DeepSeek R1 Distill Llama 70B", Upstream: "deepseek/deepseek-r1-distill-llama-70b"},
	{Alias: "deepseek/deepseek-v3.1-terminus", DisplayName: "DeepSeek V3.1 Terminus", Upstream: "deepseek/deepseek-v3.1-terminus"},
	{Alias: "deepseek/deepseek-v3.2", DisplayName: "DeepSeek V3.2", Upstream: "deepseek/deepseek-v3.2"},
	{Alias: "deepseek/deepseek-v3.2-exp", DisplayName: "DeepSeek V3.2 Exp", Upstream: "deepseek/deepseek-v3.2-exp"},
	{Alias: "deepseek/deepseek-v4-flash", DisplayName: "DeepSeek V4 Flash 0423", Upstream: "deepseek/deepseek-v4-flash"},
	{Alias: "deepseek/deepseek-v4-flash-0731", DisplayName: "DeepSeek V4 Flash 0731", Upstream: "deepseek/deepseek-v4-flash-0731"},
	{Alias: "deepseek/deepseek-v4-pro", DisplayName: "DeepSeek V4 Pro", Upstream: "deepseek/deepseek-v4-pro"},
	{Alias: "~deepseek/deepseek-v4-flash-latest", DisplayName: "DeepSeek V4 Flash Latest", Upstream: "~deepseek/deepseek-v4-flash-latest"},
}

// ClaudeProviderSpecs returns the registry used for Claude model discovery,
// default selection, and effort configuration.
func ClaudeProviderSpecs() map[string]ClaudeProviderSpec {
	return map[string]ClaudeProviderSpec{
		"deepseek": {
			DefaultModel:  DefaultClaudeUpstreamModel,
			DefaultEffort: DefaultClaudeEffort,
			Models:        deepseekClaudeModels,
		},
		"opencode-zen": {
			DefaultModel:  DefaultClaudeUpstreamModel,
			DefaultEffort: DefaultClaudeEffort,
			Models:        opencodeZenClaudeModels,
		},
		"opencode-go": {
			DefaultModel:  DefaultClaudeUpstreamModel,
			DefaultEffort: DefaultClaudeEffort,
			Models:        opencodeGoClaudeModels,
		},
		"openrouter": {
			DefaultModel:  "deepseek/" + DefaultClaudeUpstreamModel,
			DefaultEffort: DefaultClaudeEffort,
			Models:        append(append([]ClaudeModel{}, openRouterClaudeModels...), openRouterDeepSeekModels...),
		},
	}
}

func ClaudeProviderSpecFor(providerID string) (ClaudeProviderSpec, bool) {
	spec, ok := ClaudeProviderSpecs()[providerID]
	return spec, ok
}

// ClaudeProxyProviderID returns the proxy.toml provider ID used for a
// Claude-side provider preset. DeepSeek keeps its legacy deepseek-anthropic
// section; the newer Anthropic-native presets use their own provider ID.
func ClaudeProxyProviderID(providerID string) string {
	if providerID == "deepseek" {
		return DeepSeekAnthropicProviderID
	}
	return providerID
}

// ClaudeProviderModels returns the curated Claude-facing model list for an
// Anthropic-native provider, in picker order.
func ClaudeProviderModels(providerID string) []ClaudeModel {
	if spec, ok := ClaudeProviderSpecFor(providerID); ok {
		return append([]ClaudeModel(nil), spec.Models...)
	}
	return nil
}

// ClaudeModelFor resolves a model by its Claude-facing alias or upstream id.
func ClaudeModelFor(providerID, model string) (ClaudeModel, bool) {
	model = strings.TrimSuffix(model, "[1m]")
	for _, m := range ClaudeProviderModels(providerID) {
		if m.Alias == model || m.Upstream == model {
			return m, true
		}
	}
	if providerID == "deepseek" && ValidDeepSeekUpstreamModel(model) {
		alias, name := ClaudeDeepSeekAlias(model)
		return ClaudeModel{Alias: alias, DisplayName: name, Upstream: model, ContextWindow: deepSeekV4ContextWindow}, true
	}
	return ClaudeModel{}, false
}

// ClaudeModelDisplayName returns the Claude-facing display name for an alias,
// falling back to the alias itself.
func ClaudeModelDisplayName(providerID, alias string) string {
	if m, ok := ClaudeModelFor(providerID, alias); ok {
		return m.DisplayName
	}
	return alias
}

// ClaudeModelAliases returns the Claude-facing aliases in picker order.
func ClaudeModelAliases(providerID string) []string {
	models := ClaudeProviderModels(providerID)
	aliases := make([]string, 0, len(models))
	for _, m := range models {
		aliases = append(aliases, m.Alias)
	}
	return aliases
}

// ResolveClaudeSwitchModel resolves the model argument for 'aix claude
// <provider> [model]'. Empty input selects the preset default. OpenRouter also
// accepts any vendor/model slug, which is passed through to the upstream.
func ResolveClaudeSwitchModel(providerID, model string) (ClaudeModel, error) {
	if model == "" {
		if spec, ok := ClaudeProviderSpecFor(providerID); ok {
			if m, ok := ClaudeModelFor(providerID, spec.DefaultModel); ok {
				return m, nil
			}
		}
		return ClaudeModel{}, fmt.Errorf("provider %q has no default Claude model", providerID)
	}
	if m, ok := ClaudeModelFor(providerID, model); ok {
		return m, nil
	}
	if providerID == "openrouter" && strings.Contains(model, "/") {
		return ClaudeModel{Alias: model, DisplayName: model, Upstream: model}, nil
	}
	aliases := ClaudeModelAliases(providerID)
	if aliases == nil {
		aliases = []string{}
	}
	return ClaudeModel{}, fmt.Errorf("unsupported model %q (use %s)", model, strings.Join(aliases, ", "))
}

// ProviderPreset holds the pre-configured defaults for a provider. It is the
// single source of truth for auto-generating per-app provider templates.
type ProviderPreset struct {
	Name     string
	Upstream string
	// AnthropicUpstream is the proxy.toml upstream for the Anthropic
	// Messages API endpoint (e.g. https://api.deepseek.com/anthropic).
	// Empty for providers that do not speak the Anthropic protocol.
	AnthropicUpstream string
	Models            map[string]string
	CodexModel        string
	CodexAnyModel     bool
	EnvVar            string
	// AnthropicNative marks providers that natively speak the Anthropic
	// Messages API (an /anthropic-compatible upstream). These are the only
	// providers supported for the Claude Code and Claude Desktop clients;
	// everything else would require protocol conversion, which aix does not
	// do by design.
	AnthropicNative bool
}

// KnownProviders lists all providers that aix can auto-configure.
func KnownProviders() map[string]ProviderPreset {
	return map[string]ProviderPreset{
		"deepseek": {
			Name:              "DeepSeek",
			Upstream:          "https://api.deepseek.com",
			AnthropicUpstream: "https://api.deepseek.com/anthropic",
			Models: map[string]string{
				"deepseek-v4-pro": "deepseek-v4-pro",
			},
			CodexModel:      "deepseek-v4-pro",
			EnvVar:          "DEEPSEEK_API_KEY",
			AnthropicNative: true,
		},
		"kimi": {
			Name:     "Kimi (Moonshot)",
			Upstream: "https://api.moonshot.ai",
			Models: map[string]string{
				"kimi-k2.7-code": "kimi-k2.7-code",
			},
			CodexModel: "kimi-k2.7-code",
			EnvVar:     "KIMI_API_KEY",
		},
		"qwen": {
			Name:     "Qwen (Tongyi)",
			Upstream: "https://dashscope.aliyuncs.com/compatible-mode/v1",
			Models: map[string]string{
				"qwen-max": "qwen-max",
			},
			CodexModel: "qwen-max",
			EnvVar:     "QWEN_API_KEY",
		},
		"opencode-zen": {
			Name:              "OpenCode Zen",
			Upstream:          "https://opencode.ai/zen",
			AnthropicUpstream: "https://opencode.ai/zen",
			CodexModel:        "gpt-5.3-codex",
			EnvVar:            "OPENCODE_ZEN_API_KEY",
			AnthropicNative:   true,
		},
		"opencode-go": {
			Name:              "OpenCode Go",
			Upstream:          "https://opencode.ai/zen/go",
			AnthropicUpstream: "https://opencode.ai/zen/go",
			CodexModel:        "deepseek-v4-flash-vision-exp",
			CodexAnyModel:     true,
			EnvVar:            "OPENCODE_GO_API_KEY",
			AnthropicNative:   true,
		},
		"openrouter": {
			Name:              "OpenRouter",
			Upstream:          "https://openrouter.ai/api",
			AnthropicUpstream: "https://openrouter.ai/api",
			CodexModel:        "deepseek/deepseek-v4-flash-vision-exp",
			CodexAnyModel:     true,
			EnvVar:            "OPENROUTER_API_KEY",
			AnthropicNative:   true,
		},
	}
}

// IsAnthropicNativeProvider reports whether providerID natively speaks the
// Anthropic Messages API. Built-in presets carry the flag explicitly; custom
// providers qualify when their proxy.toml upstream exposes an
// Anthropic-compatible endpoint (contains "/anthropic").
func IsAnthropicNativeProvider(providerID string) bool {
	if preset, ok := KnownProviders()[providerID]; ok {
		return preset.AnthropicNative
	}
	cfg, err := LoadProxyConfig()
	if err != nil {
		return false
	}
	if p := cfg.Providers[providerID]; p != nil {
		return p.AnthropicNative || strings.Contains(p.Upstream, "/anthropic")
	}
	return false
}

// ProviderTemplateContent returns the per-app provider template for a known
// preset. Unknown providers have no generated template.
func ProviderTemplateContent(appID, providerID string, preset ProviderPreset) string {
	app, err := ResolveHarness(appID)
	if err != nil {
		return ""
	}
	return app.TemplateContent(providerID, preset)
}

// CustomProviderTemplateContent returns the per-app template for an
// OpenAI-compatible custom provider exposed through the local proxy.
func CustomProviderTemplateContent(appID, providerID, displayName, defaultModel, listen string) string {
	app, err := ResolveHarness(appID)
	if err != nil {
		return ""
	}
	return app.CustomTemplateContent(providerID, displayName, defaultModel, listen)
}

// EnsureCustomProviderTemplates writes per-app templates for a custom
// OpenAI-compatible provider, preserving any existing files. Returns the list
// of created "app/provider.toml" paths.
func EnsureCustomProviderTemplates(providerID, displayName, listen string, modelIDs []string) ([]string, error) {
	if len(modelIDs) == 0 {
		return nil, nil
	}
	var created []string
	for _, app := range AllApps() {
		path := ProviderPath(app.ID, providerID)
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return created, err
		}
		if err := os.MkdirAll(AppDir(app.ID), 0755); err != nil {
			return created, err
		}
		content := app.CustomTemplateContent(providerID, displayName, modelIDs[0], listen)
		if content == "" {
			continue
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			return created, err
		}
		created = append(created, fmt.Sprintf("%s/%s.toml", app.ID, providerID))
	}
	return created, nil
}

// EnsureProviderTemplate creates (or refreshes a stale Codex template) for a
// known provider, so 'aix set' works even when the per-app template was
// deleted or predates the current preset. Returns true when a file was
// written. Unknown providers are left untouched.
func EnsureProviderTemplate(appID, providerName string) (bool, error) {
	if preset, ok := KnownProviders()[providerName]; ok {
		return ensureTemplateWithPreset(appID, providerName, preset)
	}
	if spec, ok := NativeProvider(providerName); ok {
		return ensureTemplateWithSpec(appID, providerName, spec)
	}
	return false, nil
}

func ensureTemplateWithPreset(appID, providerName string, preset ProviderPreset) (bool, error) {
	// Claude clients only support providers that natively speak the Anthropic
	// Messages API; never generate templates that would need conversion.
	if (appID == "claudecode" || appID == "desktop") && !preset.AnthropicNative {
		return false, nil
	}
	// Codex only supports native Responses API providers; never generate a
	// template for a provider that would require protocol conversion.
	if appID == "codex" && !IsNativeProvider(providerName) {
		return false, nil
	}
	path := ProviderPath(appID, providerName)
	if path == "" {
		return false, fmt.Errorf("invalid provider name")
	}
	if _, err := os.Stat(path); err == nil {
		stale := false
		switch appID {
		case "codex":
			stale = codexTemplateStale(path, providerName, preset)
		case "claudecode":
			stale = claudeTemplateStale(path, providerName)
		}
		if !stale {
			return false, nil
		}
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := os.MkdirAll(AppDir(appID), 0755); err != nil {
		return false, err
	}
	content := ProviderTemplateContent(appID, providerName, preset)
	if content == "" {
		return false, nil
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		return false, err
	}
	return true, nil
}

func ensureTemplateWithSpec(appID, providerName string, spec NativeProviderSpec) (bool, error) {
	app, err := ResolveHarness(appID)
	if err != nil {
		return false, err
	}
	path := ProviderPath(appID, providerName)
	if path == "" {
		return false, fmt.Errorf("invalid provider name")
	}
	if _, err := os.Stat(path); err == nil {
		if !codexTemplateStale(path, providerName, ProviderPreset{CodexModel: spec.DefaultModel, CodexAnyModel: spec.AllowAnyModel}) {
			return false, nil
		}
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := os.MkdirAll(AppDir(appID), 0755); err != nil {
		return false, err
	}
	content := app.NativeTemplateContent(providerName, spec)
	if content == "" {
		return false, nil
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		return false, err
	}
	return true, nil
}

// claudeTemplateStale reports whether an existing Claude Code template predates
// the current canonical env, e.g. references tier aliases that are no longer
// mapped (claude-haiku-4-5, claude-opus-4-8) or a sonnet alias outside the
// provider's curated Claude models. Stale templates are regenerated from the
// preset.
func claudeTemplateStale(path, providerName string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var s map[string]interface{}
	if err := toml.Unmarshal(data, &s); err != nil {
		return false
	}
	env, _ := s["env"].(map[string]interface{})
	if env == nil {
		return false
	}
	for _, staleKey := range []string{"ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "CLAUDE_CODE_SUBAGENT_MODEL"} {
		if _, ok := env[staleKey]; ok {
			return true
		}
	}
	if m, _ := env["ANTHROPIC_DEFAULT_SONNET_MODEL"].(string); m != "" {
		if _, err := ResolveHarnessSelection(HarnessClaude, providerName, m, ""); err != nil {
			// OpenRouter templates may carry an arbitrary vendor/model slug.
			if providerName != "openrouter" || !strings.Contains(m, "/") {
				return true
			}
		}
	}
	return false
}

// codexTemplateStale reports whether an existing Codex template predates the
// current managed Responses passthrough contract.
func codexTemplateStale(path, providerName string, preset ProviderPreset) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var s map[string]interface{}
	if err := toml.Unmarshal(data, &s); err != nil {
		return false
	}
	// Codex provider templates must use the AIX gateway. The gateway preserves
	// the Responses protocol end to end and does not perform conversion.
	if IsNativeProvider(providerName) {
		if mode, _ := s["mode"].(string); mode != "proxy" {
			return true
		}
	}
	model, _ := s["model"].(string)
	if model == "" {
		return true
	}
	// Providers that accept any model (e.g. OpenRouter) only need a non-empty
	// template model; user-chosen arbitrary slugs must not be clobbered.
	if preset.CodexAnyModel {
		return false
	}
	if model == preset.CodexModel {
		return false
	}
	for src, dst := range preset.Models {
		if model == src || model == dst {
			return false
		}
	}
	return true
}

// templatePathOrCreate resolves a provider template, auto-generating it for
// known providers when missing.
func templatePathOrCreate(appID, providerName string) (string, error) {
	path := ProviderPath(appID, providerName)
	if path == "" {
		return "", fmt.Errorf("invalid provider name")
	}
	// EnsureProviderTemplate creates missing templates and regenerates stale
	// ones (e.g. Claude Code templates that still reference removed aliases).
	if _, err := EnsureProviderTemplate(appID, providerName); err != nil {
		return "", err
	}
	return path, nil
}
