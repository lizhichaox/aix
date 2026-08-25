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
							return "proxy", "", url
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
							if provider, ok := cfg.Providers[p]; ok && provider != nil {
								if mapped, ok := provider.Models[model]; ok {
									detail = mapped
								}
							}
						}
						detail += " via " + p
					}
					return "responses", p, detail
				}
			}
		}
	case "excalidraw":
		if baseURL, err := excalidrawActiveBaseURL(); err == nil && baseURL != "" {
			detail := "chat completions → " + baseURL
			if isLocalProxyURL(baseURL) {
				return "proxy", "", detail
			}
			return "direct", "", detail
		}
		return "proxy", "", "chat completions"
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
		// Codex only supports native providers; non-native presets never get
		// a proxy template (they would require protocol conversion).
		return ""
	case "excalidraw":
		return a.CustomTemplateContent(providerID, preset.Name, preset.CodexModel, "127.0.0.1:2026")
	}
	return ""
}

// NativeTemplateContent returns the codex template for a native provider.
func (a *HarnessInfo) NativeTemplateContent(providerID string, spec NativeProviderSpec) string {
	if a.ID != "codex" {
		return ""
	}
	return fmt.Sprintf(`description = "%s native Responses API (no proxy)"
mode = "native"
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
