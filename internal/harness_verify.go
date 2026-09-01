package internal

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// VerifyClaudeProviderApplied reads both client configurations back from disk
// without consulting AIX state. State is committed only after this succeeds.
func VerifyClaudeProviderApplied(providerID, clientModel, effort string) error {
	code, err := ResolveHarness("claudecode")
	if err != nil {
		return err
	}
	if mode, _, detail := code.StatusMode(); mode != "gateway" {
		return fmt.Errorf("Claude Code configuration is %s (%s), want gateway", mode, detail)
	}
	desktop, err := ResolveHarness("desktop")
	if err != nil {
		return err
	}
	if mode, _, detail := desktop.StatusMode(); mode != "gateway" {
		return fmt.Errorf("Claude Desktop configuration is %s (%s), want gateway", mode, detail)
	}

	raw, err := os.ReadFile(ClaudeSettingsPath())
	if err != nil {
		return fmt.Errorf("read Claude Code settings: %w", err)
	}
	var settings map[string]interface{}
	if err := json.Unmarshal(raw, &settings); err != nil {
		return fmt.Errorf("parse Claude Code settings: %w", err)
	}
	env, _ := settings["env"].(map[string]interface{})
	wantBaseURL, wantGatewayKey, err := expectedClaudeGateway(providerID)
	if err != nil {
		return err
	}
	if got, _ := env["ANTHROPIC_BASE_URL"].(string); got != wantBaseURL {
		return fmt.Errorf("Claude Code ANTHROPIC_BASE_URL = %q, want %q", got, wantBaseURL)
	}
	if clientModel != "" {
		got, _ := env["ANTHROPIC_DEFAULT_SONNET_MODEL"].(string)
		if strings.TrimSuffix(got, "[1m]") != strings.TrimSuffix(clientModel, "[1m]") {
			return fmt.Errorf("Claude Code model = %q, want %q", got, clientModel)
		}
	}
	if effort != "" {
		if got, _ := env["CLAUDE_CODE_EFFORT_LEVEL"].(string); got != effort {
			return fmt.Errorf("Claude Code effort = %q, want %q", got, effort)
		}
	}

	id, ok := desktop3pAppliedEntryIDFromDisk()
	if !ok {
		return fmt.Errorf("Claude Desktop applied gateway entry is missing")
	}
	entryRaw, err := os.ReadFile(ClaudeDesktop3pEntryPath(id))
	if err != nil {
		return fmt.Errorf("read Claude Desktop gateway entry: %w", err)
	}
	var entry map[string]interface{}
	if err := json.Unmarshal(entryRaw, &entry); err != nil {
		return fmt.Errorf("parse Claude Desktop gateway entry: %w", err)
	}
	if got, _ := entry["inferenceGatewayBaseUrl"].(string); got != wantBaseURL {
		return fmt.Errorf("Claude Desktop gateway URL = %q, want %q", got, wantBaseURL)
	}
	if got, _ := entry["inferenceGatewayApiKey"].(string); got != wantGatewayKey {
		return fmt.Errorf("Claude Desktop gateway key does not match the active AIX gateway")
	}
	return nil
}

func expectedClaudeGateway(providerID string) (string, string, error) {
	cfg, err := LoadProxyConfig()
	if err != nil {
		return "", "", fmt.Errorf("load AIX gateway config: %w", err)
	}
	listen := cfg.Listen
	if strings.HasPrefix(listen, "0.0.0.0:") {
		listen = "127.0.0.1:" + strings.TrimPrefix(listen, "0.0.0.0:")
	}
	baseURL := "http://" + listen
	if providerID != "deepseek" {
		baseURL += "/" + ClaudeProxyProviderID(providerID)
	}
	return baseURL, cfg.GatewayKey, nil
}

// VerifyCodexProviderApplied reads the effective provider/model/effort and
// private gateway route back from config.toml. It does not trust state.toml.
func VerifyCodexProviderApplied(providerID, model, effort string) error {
	config, err := readTomlMap(CodexConfigPath())
	if err != nil {
		return fmt.Errorf("read Codex configuration: %w", err)
	}
	if got, _ := config["model_provider"].(string); got != providerID {
		return fmt.Errorf("Codex model_provider = %q, want %q", got, providerID)
	}
	if got, _ := config["model"].(string); got != model {
		return fmt.Errorf("Codex model = %q, want %q", got, model)
	}
	if got, _ := config["model_reasoning_effort"].(string); got != effort {
		return fmt.Errorf("Codex effort = %q, want %q", got, effort)
	}
	providers, _ := config["model_providers"].(map[string]interface{})
	provider, _ := providers[providerID].(map[string]interface{})
	baseURL, _ := provider["base_url"].(string)
	wantRoute := "/" + CodexProxyProviderID(providerID) + "/v1"
	if !strings.HasSuffix(strings.TrimRight(baseURL, "/"), wantRoute) {
		return fmt.Errorf("Codex provider base_url = %q, want private route suffix %q", baseURL, wantRoute)
	}
	if wireAPI, _ := provider["wire_api"].(string); wireAPI != "responses" {
		return fmt.Errorf("Codex provider wire_api = %q, want responses", wireAPI)
	}
	catalogPath, _ := config["model_catalog_json"].(string)
	if catalogPath == "" {
		return fmt.Errorf("Codex model_catalog_json is missing")
	}
	if err := verifyCodexModelCatalog(catalogPath, model); err != nil {
		return err
	}
	return nil
}

// verifyCodexModelCatalog checks the catalog invariants AIX depends on before
// a provider transaction is committed and the desktop host is relaunched.
// Codex owns the full schema; these checks cover fields AIX generates and a
// known current-host incompatibility that plain JSON decoding cannot detect.
func verifyCodexModelCatalog(path, activeModel string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read Codex model catalog: %w", err)
	}
	var catalog struct {
		Models []map[string]json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		return fmt.Errorf("parse Codex model catalog: %w", err)
	}
	if len(catalog.Models) == 0 {
		return fmt.Errorf("Codex model catalog has no models")
	}
	foundActive := false
	for i, entry := range catalog.Models {
		var slug string
		if err := json.Unmarshal(entry["slug"], &slug); err != nil || strings.TrimSpace(slug) == "" {
			return fmt.Errorf("Codex model catalog entry %d has an invalid slug", i)
		}
		if slug == activeModel {
			foundActive = true
		}
		if value, present := entry["web_search_tool_type"]; present {
			var decoded interface{}
			if err := json.Unmarshal(value, &decoded); err != nil {
				return fmt.Errorf("Codex model catalog entry %q has invalid web_search_tool_type; omit it when unsupported", slug)
			}
			switch decoded.(type) {
			case string, map[string]interface{}:
			default:
				return fmt.Errorf("Codex model catalog entry %q has invalid web_search_tool_type; omit it when unsupported", slug)
			}
		}
	}
	if !foundActive {
		return fmt.Errorf("Codex model catalog does not contain active model %q", activeModel)
	}
	return nil
}

// VerifyCodexNativeRestored confirms Codex no longer points at an AIX-managed
// provider or private gateway route. The native OpenAI model itself may be a
// user-selected value restored from the snapshot.
func VerifyCodexNativeRestored() error {
	config, err := readTomlMap(CodexConfigPath())
	if err != nil {
		return fmt.Errorf("read Codex configuration: %w", err)
	}
	providerID, _ := config["model_provider"].(string)
	if providerID != "" && providerID != "openai" {
		return fmt.Errorf("Codex model_provider = %q after native restore", providerID)
	}
	if providers, _ := config["model_providers"].(map[string]interface{}); providers != nil {
		for id, raw := range providers {
			provider, _ := raw.(map[string]interface{})
			baseURL, _ := provider["base_url"].(string)
			if strings.Contains(baseURL, "/"+CodexProxyProviderID(id)+"/v1") {
				return fmt.Errorf("Codex provider %q still points at an AIX private route", id)
			}
		}
	}
	return nil
}
