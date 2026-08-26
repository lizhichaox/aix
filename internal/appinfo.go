package internal

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Restore resets the app's configuration to its native state.
func (a *HarnessInfo) Restore() error {
	switch a.ID {
	case "claudecode":
		return restoreClaudeCodeNative()
	case "desktop":
		return restoreDesktopNative()
	case "codex":
		return restoreCodexNative()
	case "excalidraw":
		return restoreExcalidrawNative()
	}
	return fmt.Errorf("unknown app '%s'", a.ID)
}

// StatusMode returns the app's current connection mode, effective provider
// (when it differs from the state), and a human-readable detail string.
func (a *HarnessInfo) StatusMode() (mode, provider, detail string) {
	switch a.ID {
	case "claudecode":
		if data, err := os.ReadFile(ClaudeSettingsPath()); err == nil {
			var s map[string]interface{}
			if json.Unmarshal(data, &s) == nil {
				if env, ok := s["env"].(map[string]interface{}); ok {
					if url, ok := env["ANTHROPIC_BASE_URL"].(string); ok && url != "" {
						if isLocalProxyURL(url) {
							return "gateway", "", url
						}
						return "direct", "", url
					}
					return "direct", "", "Anthropic"
				}
			}
		}
	case "desktop":
		// Current Claude Desktop builds activate third-party mode from the
		// config library in the Claude-3p data directory; the legacy flat
		// inferenceGateway* fields in the main config are no longer read.
		if id, ok := desktop3pAppliedEntryIDFromDisk(); ok {
			if data, err := os.ReadFile(ClaudeDesktop3pEntryPath(id)); err == nil {
				var e map[string]interface{}
				if json.Unmarshal(data, &e) == nil {
					if p, _ := e["inferenceProvider"].(string); p == "gateway" {
						detail := ""
						if gw, ok := e["inferenceGatewayBaseUrl"].(string); ok {
							detail = "3p → " + gw
						}
						return "gateway", "", detail
					}
					return "native", "", "Anthropic"
				}
			}
		}
		if data, err := os.ReadFile(ClaudeDesktopConfigPath()); err == nil {
			var s map[string]interface{}
			if json.Unmarshal(data, &s) == nil {
				if mode, ok := s["deploymentMode"].(string); ok && mode == "3p" {
					detail := ""
					if gw, ok := s["inferenceGatewayBaseUrl"].(string); ok {
						detail = "3p → " + gw
					}
					return "gateway", "", detail
				}
				return "native", "", "Anthropic"
			}
		}
	case "codex":
		if data, err := os.ReadFile(CodexConfigPath()); err == nil {
			var s map[string]interface{}
			if toml.Unmarshal(data, &s) == nil {
				if model, ok := s["model"].(string); ok && model != "" {
					p, _ := s["model_provider"].(string)
					detail := model
					if p != "" {
						if cfg, err := LoadProxyConfig(); err == nil {
							if provider, ok := cfg.Providers[CodexProxyProviderID(p)]; ok && provider != nil {
								if mapped, ok := provider.Models[model]; ok {
									detail = mapped
								}
							}
						}
						detail += " via " + p
					}
					mode := "responses"
					if providers, ok := s["model_providers"].(map[string]interface{}); ok {
						if raw, ok := providers[p].(map[string]interface{}); ok {
							if baseURL, _ := raw["base_url"].(string); isLocalProxyURL(baseURL) {
								mode = "gateway"
							}
						}
					}
					return mode, p, detail
				}
			}
		}
	case "excalidraw":
		if baseURL, err := excalidrawActiveBaseURL(); err == nil && baseURL != "" {
			detail := "chat completions → " + baseURL
			if isLocalProxyURL(baseURL) {
				return "gateway", "", detail
			}
			return "direct", "", detail
		}
		return "gateway", "", "chat completions"
	}
	return "", "", ""
}

// CurrentHarnessEffort reads the effort currently written to a harness's
// native configuration. It returns an empty string when the harness owns the
// setting or no explicit effort is available.
func CurrentHarnessEffort(harnessID string) string {
	switch harnessID {
	case HarnessClaude:
		data, err := os.ReadFile(ClaudeSettingsPath())
		if err != nil {
			return ""
		}
		var settings map[string]interface{}
		if json.Unmarshal(data, &settings) != nil {
			return ""
		}
		env, _ := settings["env"].(map[string]interface{})
		effort, _ := env["CLAUDE_CODE_EFFORT_LEVEL"].(string)
		return strings.TrimSpace(effort)
	case HarnessCodex:
		data, err := os.ReadFile(CodexConfigPath())
		if err != nil {
			return ""
		}
		var config map[string]interface{}
		if toml.Unmarshal(data, &config) != nil {
			return ""
		}
		effort, _ := config["model_reasoning_effort"].(string)
		return strings.TrimSpace(effort)
	}
	return ""
}

// CurrentHarnessModel returns the model currently written to a harness and
// its known upstream context window. Managed Claude aliases are resolved back
// to the provider model so public status output does not expose internal
// compatibility names. A zero context window means the active catalog does
// not declare one.
func CurrentHarnessModel(harnessID, providerID string) (string, int) {
	model := ""
	switch harnessID {
	case HarnessClaude:
		data, err := os.ReadFile(ClaudeSettingsPath())
		if err == nil {
			var settings map[string]interface{}
			if json.Unmarshal(data, &settings) == nil {
				env, _ := settings["env"].(map[string]interface{})
				model, _ = env["ANTHROPIC_DEFAULT_SONNET_MODEL"].(string)
				if strings.TrimSpace(model) == "" {
					// Native Claude stores its active family/model at the top
					// level (commonly "sonnet", "opus", or "haiku"). Managed
					// providers keep using the explicit environment mapping above.
					model, _ = settings["model"].(string)
				}
			}
		}
	case HarnessCodex:
		data, err := os.ReadFile(CodexConfigPath())
		if err == nil {
			var config map[string]interface{}
			if toml.Unmarshal(data, &config) == nil {
				model, _ = config["model"].(string)
			}
		}
	}
	model = strings.TrimSpace(model)
	if harnessID == HarnessClaude && providerID == "anthropic" {
		if active, ok := activeClaudeModel(); ok {
			model = active
		}
	}
	if providerID != "" && providerID != "-" && providerID != "anthropic" && providerID != "openai" {
		selection, err := ResolveHarnessSelection(harnessID, providerID, model, "")
		if err == nil {
			return selection.UpstreamModel, selection.ContextWindow
		}
	}
	if harnessID == HarnessCodex && providerID == "openai" {
		return model, codexNativeModelContext(model)
	}
	if harnessID == HarnessClaude && providerID == "anthropic" {
		return strings.TrimSuffix(model, "[1m]"), claudeNativeModelContext(model)
	}
	return strings.TrimSuffix(model, "[1m]"), 0
}

// activeClaudeModel returns the most recently updated model that Claude
// records in ~/.claude.json. Claude 2.x persists the active model per session
// in clientDataCacheSlots instead of settings.json, so status reads this live
// value and falls back to settings.json when it is unavailable.
func activeClaudeModel() (string, bool) {
	raw, err := os.ReadFile(ClaudeConfigJSONPath())
	if err != nil {
		return "", false
	}
	var cfg struct {
		ClientDataCacheSlots map[string]struct {
			Model string          `json:"model"`
			At    json.RawMessage `json:"at"`
		} `json:"clientDataCacheSlots"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return "", false
	}
	best := ""
	bestKey := ""
	var bestAt int64
	for key, slot := range cfg.ClientDataCacheSlots {
		model := strings.TrimSpace(slot.Model)
		if model == "" {
			continue
		}
		at := parseEpochMillis(slot.At)
		if bestKey == "" || at > bestAt || (at == bestAt && key > bestKey) {
			best = model
			bestKey = key
			bestAt = at
		}
	}
	return best, bestKey != ""
}

// parseEpochMillis reads a numeric or string epoch timestamp from JSON and
// returns it in milliseconds. Only relative ordering matters here, so a
// missing or malformed value collapses to zero.
func parseEpochMillis(raw json.RawMessage) int64 {
	if len(raw) == 0 {
		return 0
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err == nil {
		return n
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		var v int64
		if _, err := fmt.Sscanf(strings.TrimSpace(s), "%d", &v); err == nil {
			return v
		}
		return 0
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		return int64(f)
	}
	return 0
}

// claudeNativeModelContext supplies the documented context window for
// Anthropic's native rolling family aliases and current model IDs. Unlike
// Codex, Claude does not persist a local model catalog, so this deliberately
// narrow fallback avoids guessing for unknown or legacy model names.
func claudeNativeModelContext(model string) int {
	model = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(model), "[1m]"))
	switch model {
	case "sonnet", "opus", "fable", "mythos",
		"claude-sonnet-5", "claude-sonnet-4-6",
		"claude-opus-5", "claude-opus-4-8", "claude-opus-4-7", "claude-opus-4-6",
		"claude-fable-5", "claude-mythos-5":
		return 1_000_000
	case "haiku", "claude-haiku-4-5", "claude-haiku-4-5-20251001":
		return 200_000
	default:
		return 0
	}
}

// codexNativeModelContext reads Codex's own server-refreshed model catalog.
// The cache remains authoritative for OpenAI-native models so AIX does not
// need to hard-code model metadata that can change between Codex releases.
func codexNativeModelContext(model string) int {
	if strings.TrimSpace(model) == "" {
		return 0
	}
	raw, err := os.ReadFile(CodexModelsCachePath())
	if err != nil {
		return 0
	}
	var catalog struct {
		Models []struct {
			Slug          string `json:"slug"`
			ContextWindow int    `json:"context_window"`
		} `json:"models"`
	}
	if json.Unmarshal(raw, &catalog) != nil {
		return 0
	}
	for _, entry := range catalog.Models {
		if entry.Slug == model {
			return entry.ContextWindow
		}
	}
	return 0
}

// isLocalProxyURL reports whether u points at the local AIX proxy listener
// (127.0.0.1 / localhost) rather than a remote upstream.
func isLocalProxyURL(u string) bool {
	parsed, err := url.Parse(u)
	if err != nil {
		return strings.Contains(u, "127.0.0.1") || strings.Contains(u, "localhost")
	}
	switch parsed.Hostname() {
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	return false
}

// excalidrawActiveBaseURL returns the baseURL of the AI profile the Excalidraw
// plugin currently uses (resolved from the default text model), or the first
// profile pointing at the AIX proxy as a fallback.
func excalidrawActiveBaseURL() (string, error) {
	vaults, err := findExcalidrawVaults()
	if err != nil {
		return "", err
	}
	for _, vault := range vaults {
		path := filepath.Join(vault, ".obsidian", "plugins", "obsidian-excalidraw-plugin", "data.json")
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var data map[string]interface{}
		if err := json.Unmarshal(raw, &data); err != nil {
			continue
		}
		if baseURL := excalidrawProfileBaseURL(data); baseURL != "" {
			return baseURL, nil
		}
	}
	return "", fmt.Errorf("no excalidraw base URL found")
}

// excalidrawProfileBaseURL resolves the provider profile used by the default
// text model, falling back to any profile pointing at the AIX proxy.
func excalidrawProfileBaseURL(data map[string]interface{}) string {
	profiles, _ := data["aiProviderProfiles"].(map[string]interface{})
	if profiles == nil {
		return ""
	}

	providerID := "aix-proxy"
	if model, ok := data["aiDefaultTextModel"].(string); ok {
		if configs, ok := data["aiTextModelConfigs"].(map[string]interface{}); ok {
			if mc, ok := configs[model].(map[string]interface{}); ok {
				if pid, ok := mc["providerId"].(string); ok && pid != "" {
					providerID = pid
				}
			}
		}
	}
	if p, ok := profiles[providerID].(map[string]interface{}); ok {
		if b, ok := p["baseURL"].(string); ok && b != "" {
			return b
		}
	}

	for _, pv := range profiles {
		if pm, ok := pv.(map[string]interface{}); ok {
			if b, ok := pm["baseURL"].(string); ok && isLocalProxyURL(b) {
				return b
			}
		}
	}
	return ""
}

// ValidateTemplate checks an app's provider template against the current
// proxy gateway key. It returns a display detail and whether the check passed.
func (a *HarnessInfo) ValidateTemplate(data map[string]interface{}, gatewayKey string) (string, bool) {
	switch a.ID {
	case "codex":
		if mode, _ := data["mode"].(string); mode == "native" {
			model, _ := data["model"].(string)
			if model == "" {
				return "missing model field", false
			}
			return fmt.Sprintf("model: %s (native direct)", model), true
		}
		model, _ := data["model"].(string)
		if model == "" {
			return "missing model field", false
		}
		if mpRaw, ok := data["model_providers"]; ok {
			if mp, ok := mpRaw.(map[string]interface{}); ok {
				for _, mpData := range mp {
					if pd, ok := mpData.(map[string]interface{}); ok {
						if bt, ok := pd["experimental_bearer_token"].(string); ok {
							if bt != "" && gatewayKey != "" && bt != gatewayKey {
								return fmt.Sprintf("bearer_token %q ≠ proxy gateway_key %q", bt, gatewayKey), false
							}
						}
					}
				}
			}
		}
		return fmt.Sprintf("model: %v", model), true
	case "claudecode":
		if _, ok := data["env"]; ok {
			return "has env config", true
		}
		return "direct mode (no env)", true
	case "desktop":
		mode, _ := data["deployment_mode"].(string)
		if mode == "3p" {
			// The applied 3p entry always reads the current key directly from
			// proxy.toml. A legacy gateway_key in this provider template is
			// intentionally ignored because templates may outlive key rotation.
			return "3p gateway mode", true
		}
		return "native mode", true
	}
	return "", true
}

// ClientAppName returns the macOS application to quit/relaunch for this app,
// or "" when the app has no desktop client.
func (a *HarnessInfo) ClientAppName() string {
	switch a.ID {
	case "desktop":
		return "Claude"
	case "codex":
		return CodexHostAppName()
	}
	return ""
}

// CodexHostAppName returns the macOS application hosting Codex. Current builds
// bundle Codex inside ChatGPT.app; a standalone Codex.app is preferred when
// present.
func CodexHostAppName() string {
	for _, p := range []string{
		"/Applications/Codex.app",
		filepath.Join(HomeDir(), "Applications", "Codex.app"),
		"/Applications/ChatGPT.app",
		filepath.Join(HomeDir(), "Applications", "ChatGPT.app"),
	} {
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			return strings.TrimSuffix(filepath.Base(p), ".app")
		}
	}
	return "Codex"
}

// TemplateContent returns the per-app provider template for a known preset.
func (a *HarnessInfo) TemplateContent(providerID string, preset ProviderPreset) string {
	switch a.ID {
	case "claudecode":
		base := "http://127.0.0.1:2026"
		modelID := ""
		modelName := ""
		effort := DefaultClaudeEffort
		if selection, err := ResolveHarnessSelection(HarnessClaude, providerID, "", ""); err == nil {
			modelID = selection.ClientModel
			modelName = selection.ClientModel
			effort = selection.Effort
			if spec, ok := HarnessProvider(HarnessClaude, providerID); ok {
				if model, found := spec.Models[selection.Model]; found {
					modelName = model.DisplayName
					if model.ContextWindow >= oneMillionContext {
						modelID += "[1m]"
					}
				}
			}
		}
		// Newer Anthropic-native providers (OpenCode, OpenRouter) route
		// through an explicit /<provider> prefix so the proxy can dispatch
		// even when several Anthropic-compatible sections exist. DeepSeek
		// keeps the legacy unprefixed base URL.
		if preset.AnthropicNative && providerID != "deepseek" {
			base += "/" + ClaudeProxyProviderID(providerID)
		}
		return fmt.Sprintf(`description = "%s via AIX gateway"
model = "sonnet"

[env]
ANTHROPIC_BASE_URL = "%s"
ANTHROPIC_DEFAULT_SONNET_MODEL = "%s"
ANTHROPIC_DEFAULT_SONNET_MODEL_NAME = "%s"
CLAUDE_CODE_EFFORT_LEVEL = "%s"
`, preset.Name, base, modelID, modelName, effort)
	case "desktop":
		return fmt.Sprintf(`description = "%s via aix gateway"
deployment_mode = "3p"
`, preset.Name)
	case "codex":
		if spec, ok := NativeProvider(providerID); ok {
			return a.NativeTemplateContent(providerID, spec)
		}
		// Codex only supports native Responses providers; non-native presets
		// would require forbidden protocol conversion.
		return ""
	case "excalidraw":
		return a.CustomTemplateContent(providerID, preset.Name, preset.CodexModel, "127.0.0.1:2026")
	}
	return ""
}

// NativeTemplateContent returns the Codex template for a native Responses
// provider routed through the private AIX gateway.
func (a *HarnessInfo) NativeTemplateContent(providerID string, spec NativeProviderSpec) string {
	if a.ID != "codex" {
		return ""
	}
	return fmt.Sprintf(`description = "%s via AIX gateway (Responses passthrough)"
mode = "proxy"
model = "%s"
`, spec.Name, spec.DefaultModel)
}

// CustomTemplateContent returns the per-app template for an OpenAI-compatible
// custom provider exposed through the local proxy.
func (a *HarnessInfo) CustomTemplateContent(providerID, displayName, defaultModel, listen string) string {
	switch a.ID {
	case "excalidraw":
		return fmt.Sprintf(`description = %q
model = %q
`, displayName, defaultModel)
	}
	return ""
}
