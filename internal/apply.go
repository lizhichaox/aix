package internal

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

func getApplyFunc(appID string) func(string, map[string]interface{}) error {
	switch appID {
	case "claudecode":
		return applyClaudeCodeProvider
	case "desktop":
		return applyDesktopProvider
	case "codex":
		return applyCodexProvider
	case "excalidraw":
		return applyExcalidrawProvider
	default:
		return nil
	}
}

func RestoreNative(app *HarnessInfo) error {
	_ = EnsureDirs()
	if err := app.Restore(); err != nil {
		return err
	}
	pruneBackups(BackupsDir(), backupsKeepPerLabel)
	return nil
}

var claudeCodeManagedEnvKeys = []string{
	"ANTHROPIC_API_KEY",
	"ANTHROPIC_AUTH_TOKEN",
	"ANTHROPIC_BASE_URL",
	"ANTHROPIC_DEFAULT_SONNET_MODEL",
	"ANTHROPIC_DEFAULT_SONNET_MODEL_NAME",
	"CLAUDE_CODE_EFFORT_LEVEL",
}

type managedSettingSnapshot struct {
	Present bool        `json:"present"`
	Value   interface{} `json:"value,omitempty"`
}

type claudeCodeNativeSnapshot struct {
	Version int                               `json:"version"`
	Model   managedSettingSnapshot            `json:"model"`
	Env     map[string]managedSettingSnapshot `json:"env"`
}

func saveClaudeCodeNativeSnapshot(settings map[string]interface{}) error {
	path := ClaudeCodeNativeSnapshotPath()
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	snapshot := claudeCodeNativeSnapshot{
		Version: 1,
		Env:     make(map[string]managedSettingSnapshot, len(claudeCodeManagedEnvKeys)),
	}
	if model, ok := settings["model"]; ok {
		snapshot.Model = managedSettingSnapshot{Present: true, Value: model}
	}
	env, _ := settings["env"].(map[string]interface{})
	for _, key := range claudeCodeManagedEnvKeys {
		value, ok := env[key]
		snapshot.Env[key] = managedSettingSnapshot{Present: ok, Value: value}
	}
	out, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateFile(path, append(out, '\n'))
}

func restoreClaudeCodeNative() error {
	path := ClaudeSettingsPath()
	existing := make(map[string]interface{})
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &existing); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	}
	if err := backup(path, "native"); err != nil {
		return fmt.Errorf("backup failed: %w", err)
	}

	env, _ := existing["env"].(map[string]interface{})
	if env == nil {
		env = make(map[string]interface{})
	}
	snapshotPath := ClaudeCodeNativeSnapshotPath()
	var snapshot claudeCodeNativeSnapshot
	hasSnapshot := false
	if snapshotRaw, readErr := os.ReadFile(snapshotPath); readErr == nil {
		if err := json.Unmarshal(snapshotRaw, &snapshot); err != nil {
			return fmt.Errorf("parse native Claude Code snapshot: %w", err)
		}
		if snapshot.Version != 1 || snapshot.Env == nil {
			return fmt.Errorf("unsupported native Claude Code snapshot format in %s", snapshotPath)
		}
		hasSnapshot = true
		if snapshot.Model.Present {
			existing["model"] = snapshot.Model.Value
		} else {
			delete(existing, "model")
		}
		for _, key := range claudeCodeManagedEnvKeys {
			field := snapshot.Env[key]
			if field.Present {
				env[key] = field.Value
			} else {
				delete(env, key)
			}
		}
	} else if !os.IsNotExist(readErr) {
		return fmt.Errorf("read native Claude Code snapshot: %w", readErr)
	} else {
		// Older AIX installations have no field snapshot. Remove only values AIX
		// is known to own; never discard unrelated user environment variables.
		for _, key := range claudeCodeManagedEnvKeys {
			delete(env, key)
		}
		if model, _ := existing["model"].(string); model == "sonnet" {
			delete(existing, "model")
		}
	}
	if len(env) == 0 {
		delete(existing, "env")
	} else {
		existing["env"] = env
	}
	out, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	if err := writePrivateFile(path, append(out, '\n')); err != nil {
		return err
	}
	if hasSnapshot {
		if err := os.Remove(snapshotPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("consume native Claude Code snapshot: %w", err)
		}
	}
	return nil
}

var desktopGatewayAuthFields = []string{
	"deploymentMode",
	"inferenceGatewayBaseUrl",
	"inferenceGatewayApiKey",
	"inferenceGatewayAuthScheme",
}

var codexManagedConfigFields = []string{
	"model_provider",
	"model",
	"model_reasoning_effort",
	"model_providers",
	"preferred_auth_method",
	"forced_login_method",
	"model_catalog_json",
}

type codexNativeSnapshot struct {
	Version       int                    `json:"version"`
	Fields        map[string]interface{} `json:"fields"`
	Present       map[string]bool        `json:"present"`
	Catalog       []byte                 `json:"catalog,omitempty"`
	CatalogExists bool                   `json:"catalog_exists"`
}

func codexNativeSnapshotPath(backupDir string) string {
	if filepath.Clean(backupDir) == filepath.Clean(BackupsDir()) {
		return CodexNativeSnapshotPath()
	}
	return filepath.Join(filepath.Dir(backupDir), "codex_native.json")
}

// saveCodexNativeSnapshot preserves only the fields AIX owns while a managed
// provider is active. Unrelated config changes made during that period remain
// intact when native OpenAI operation is restored.
func saveCodexNativeSnapshot(config map[string]interface{}, catalogPath, backupDir string) error {
	provider, _ := config["model_provider"].(string)
	if provider != "" && provider != "openai" {
		return nil
	}
	path := codexNativeSnapshotPath(backupDir)
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	snap := codexNativeSnapshot{
		Version: 1,
		Fields:  make(map[string]interface{}),
		Present: make(map[string]bool),
	}
	for _, key := range codexManagedConfigFields {
		if value, ok := config[key]; ok {
			snap.Fields[key] = value
			snap.Present[key] = ok
		}
	}
	if raw, err := os.ReadFile(catalogPath); err == nil {
		snap.Catalog = raw
		snap.CatalogExists = true
	} else if !os.IsNotExist(err) {
		return err
	}
	out, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateFile(path, append(out, '\n'))
}

func saveNativeDesktopSnap(config map[string]interface{}) error {
	snap := NativeDesktopSnapPath()
	out, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(snap, append(out, '\n'), 0600)
}

func restoreDesktopSnap(dest *map[string]interface{}) {
	snap := NativeDesktopSnapPath()
	raw, err := os.ReadFile(snap)
	if err != nil {
		for _, k := range desktopGatewayAuthFields {
			delete(*dest, k)
		}
		return
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		return
	}
	os.Remove(snap)
}

func restoreDesktopNative() error {
	path := ClaudeDesktopConfigPath()

	if err := backup(path, "native"); err != nil {
		return fmt.Errorf("backup failed: %w", err)
	}

	snap := NativeDesktopSnapPath()
	if snapRaw, err := os.ReadFile(snap); err == nil {
		var native map[string]interface{}
		if err := json.Unmarshal(snapRaw, &native); err != nil {
			return fmt.Errorf("parse native desktop snapshot: %w", err)
		}
		for _, k := range desktopGatewayAuthFields {
			delete(native, k)
		}
		native["deploymentMode"] = "1p"
		out, err := json.MarshalIndent(native, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal native desktop snapshot: %w", err)
		}
		if err := os.WriteFile(path, append(out, '\n'), 0600); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
		os.Remove(snap)
		if err := removeDesktop3pDir(); err != nil {
			return err
		}
		return nil
	}

	existing := make(map[string]interface{})
	if raw, err := os.ReadFile(path); err == nil {
		json.Unmarshal(raw, &existing)
	} else if bak := path + ".bak"; true {
		if bakRaw, err := os.ReadFile(bak); err == nil {
			json.Unmarshal(bakRaw, &existing)
		}
	}
	for _, k := range desktopGatewayAuthFields {
		delete(existing, k)
	}
	existing["deploymentMode"] = "1p"
	out, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if err := os.WriteFile(path, append(out, '\n'), 0600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return removeDesktop3pDir()
}

func removeDesktop3pDir() error {
	p3pDir := ClaudeDesktop3pDir()
	if _, err := os.Stat(p3pDir); os.IsNotExist(err) {
		return nil
	}
	bakDir := filepath.Join(HomeDir(), "Library", "Application Support", "Claude-3p.bak")
	if _, err := os.Stat(bakDir); err == nil {
		// A prior restore cycle left a stale backup slot (the active
		// directory was recreated by a later switch). Archive the stale
		// snapshot so the current, newer third-party data can claim the
		// canonical slot without dropping the previous snapshot.
		if err := archiveClaude3pBackup(bakDir); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat Claude-3p backup: %w", err)
	}
	if err := os.Rename(p3pDir, bakDir); err != nil {
		return fmt.Errorf("back up Claude-3p data: %w", err)
	}
	return nil
}

// archiveClaude3pBackup renames an existing Claude-3p backup directory out of
// the canonical slot so a newer data directory can take its place. The archive
// keeps the previous snapshot available and is never deleted here.
func archiveClaude3pBackup(bakDir string) error {
	ts := time.Now().Format("20060102-150405.000")
	for i := 0; ; i++ {
		archive := bakDir + "." + ts
		if i > 0 {
			archive = fmt.Sprintf("%s.%s.%d", bakDir, ts, i)
		}
		if _, err := os.Stat(archive); os.IsNotExist(err) {
			if err := os.Rename(bakDir, archive); err != nil {
				return fmt.Errorf("archive stale Claude-3p backup: %w", err)
			}
			return nil
		} else if err != nil {
			return fmt.Errorf("stat Claude-3p backup archive: %w", err)
		}
	}
}

func restoreCodexNative() error {
	return restoreCodexNativeAt(CodexConfigPath(), CodexModelsPath(), BackupsDir())
}

func restoreCodexNativeAt(path, catalogPath, backupDir string) error {
	existing, err := readTomlMap(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := backupTo(path, "native", backupDir); err != nil {
		return fmt.Errorf("backup failed: %w", err)
	}
	snapshotPath := codexNativeSnapshotPath(backupDir)
	var snapshot codexNativeSnapshot
	hasSnapshot := false
	if raw, readErr := os.ReadFile(snapshotPath); readErr == nil {
		if err := json.Unmarshal(raw, &snapshot); err != nil {
			return fmt.Errorf("parse native Codex snapshot: %w", err)
		}
		if snapshot.Version != 1 || snapshot.Fields == nil || snapshot.Present == nil {
			return fmt.Errorf("unsupported native Codex snapshot format in %s", snapshotPath)
		}
		hasSnapshot = true
		for _, key := range codexManagedConfigFields {
			if snapshot.Present[key] {
				existing[key] = snapshot.Fields[key]
			} else {
				delete(existing, key)
			}
		}
	} else if !os.IsNotExist(readErr) {
		return fmt.Errorf("read native Codex snapshot: %w", readErr)
	} else {
		// Without a snapshot, retain the historical safe fallback.
		for _, key := range codexManagedConfigFields {
			delete(existing, key)
		}
		existing["model_provider"] = "openai"
		existing["model"] = DefaultOpenAICodexModel
	}
	if err := backupTo(catalogPath, "native", backupDir); err != nil {
		return fmt.Errorf("backup model catalog: %w", err)
	}
	if hasSnapshot && snapshot.CatalogExists {
		if err := writePrivateFile(catalogPath, snapshot.Catalog); err != nil {
			return fmt.Errorf("restore native model catalog: %w", err)
		}
	} else if err := os.Remove(catalogPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove model catalog: %w", err)
	}
	if err := writeTomlPrivate(path, existing); err != nil {
		return err
	}
	if hasSnapshot {
		if err := os.Remove(snapshotPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("consume native Codex snapshot: %w", err)
		}
	}
	return nil
}

func restoreExcalidrawNative() error {
	vaults, err := findExcalidrawVaults()
	if err != nil {
		return fmt.Errorf("find vaults: %w", err)
	}
	var errs []string
	for _, vaultPath := range vaults {
		pluginDataPath := filepath.Join(vaultPath, ".obsidian", "plugins", "obsidian-excalidraw-plugin", "data.json")
		raw, err := os.ReadFile(pluginDataPath)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", vaultPath, err))
			continue
		}
		var pluginData map[string]interface{}
		if err := json.Unmarshal(raw, &pluginData); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", vaultPath, err))
			continue
		}
		if err := backup(pluginDataPath, "native"); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", vaultPath, err))
			continue
		}
		delete(pluginData, "aiEnabled")
		delete(pluginData, "aiProviderProfiles")
		delete(pluginData, "aiTextModelConfigs")
		delete(pluginData, "aiDefaultTextModel")
		out, err := json.MarshalIndent(pluginData, "", "\t")
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", vaultPath, err))
			continue
		}
		if err := os.WriteFile(pluginDataPath, out, 0600); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", vaultPath, err))
			continue
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("failed to update some vaults:\n%s", strings.Join(errs, "\n"))
	}
	return nil
}

func ApplyProvider(app *HarnessInfo, providerName string) error {
	return ApplyProviderWithSelection(app, providerName, "", "")
}

// ApplyProviderWithModel applies a provider template, optionally overriding
// the model before the app-specific writer runs.
func ApplyProviderWithModel(app *HarnessInfo, providerName, model string) error {
	return ApplyProviderWithSelection(app, providerName, model, "")
}

// ApplyProviderWithSelection applies a provider template with optional model
// and effort overrides already scoped to the target harness.
func ApplyProviderWithSelection(app *HarnessInfo, providerName, model, effort string) error {
	// Claude clients only support providers that natively speak the Anthropic
	// Messages API. Protocol conversion is out of scope by design.
	if app.ID == "claudecode" || app.ID == "desktop" {
		if !IsAnthropicNativeProvider(providerName) {
			return fmt.Errorf("provider %q does not expose an Anthropic-compatible API; only Anthropic-native providers are supported for %s (e.g. deepseek, or a custom provider with an /anthropic upstream)", providerName, app.Name)
		}
		// Auto-create the proxy.toml provider section for built-in
		// Anthropic-native presets so switching works even when the setup
		// wizard did not run.
		if preset, ok := KnownProviders()[providerName]; ok && preset.AnthropicNative {
			if err := EnsureClaudeProxyProvider(providerName); err != nil {
				return err
			}
		}
	}
	// Codex only supports native Responses API providers; the gateway passes
	// that protocol through without conversion.
	if app.ID == "codex" && !IsNativeProvider(providerName) {
		return fmt.Errorf("provider %q is not a Codex Responses provider; only native Responses API providers are supported for Codex (aix codex <provider> [model])", providerName)
	}
	if model != "" {
		if err := ValidateModelOverride(app, providerName, model); err != nil {
			return err
		}
	}
	path, err := templatePathOrCreate(app.ID, providerName)
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		providers, _ := ListProviders(app.ID)
		if len(providers) == 0 {
			return fmt.Errorf("no providers configured for %s\n  Run 'aix setup' to configure providers first", app.Name)
		}
		return fmt.Errorf("provider %q not found for %s\n  Available: %s", providerName, app.Name, strings.Join(providers, ", "))
	}
	var data map[string]interface{}
	if _, err := toml.DecodeFile(path, &data); err != nil {
		return fmt.Errorf("load provider '%s': %w", providerName, err)
	}
	if model != "" {
		data["model"] = model
	}
	if effort != "" {
		data["effort"] = effort
	}
	if app.ApplyFunc == nil {
		return fmt.Errorf("no apply function for app '%s'", app.ID)
	}
	if err := app.ApplyFunc(providerName, data); err != nil {
		return err
	}
	pruneBackups(BackupsDir(), backupsKeepPerLabel)
	return nil
}

// ApplyDeepSeekClaudeCode configures Claude Code to talk to DeepSeek through
// the AIX proxy. The Anthropic-shaped model name and display name follow the
// chosen DeepSeek upstream model. Curated models keep their familiar aliases;
// newly released models receive a stable Claude-shaped alias dynamically.
func ApplyDeepSeekClaudeCode(model string) error {
	return ApplyDeepSeekClaudeCodeWithEffort(model, "")
}

// ApplyDeepSeekClaudeCodeWithEffort applies a DeepSeek model and the resolved
// Claude harness effort. An empty effort uses the registry default.
func ApplyDeepSeekClaudeCodeWithEffort(model, effort string) error {
	if !ValidDeepSeekUpstreamModel(model) {
		return fmt.Errorf("invalid DeepSeek model id %q (expected deepseek-*)", model)
	}
	app, err := ResolveHarness("claudecode")
	if err != nil {
		return err
	}
	path, err := templatePathOrCreate(app.ID, "deepseek")
	if err != nil {
		return err
	}
	var data map[string]interface{}
	if _, err := toml.DecodeFile(path, &data); err != nil {
		return fmt.Errorf("load provider 'deepseek': %w", err)
	}
	_, name := ClaudeDeepSeekAlias(model)
	selection, err := ResolveHarnessSelection(HarnessClaude, "deepseek", model, effort)
	if err != nil {
		return err
	}
	if env, ok := data["env"].(map[string]interface{}); ok {
		cm, _ := ClaudeModelFor("deepseek", model)
		env["ANTHROPIC_DEFAULT_SONNET_MODEL"] = ClaudeCodeModelID(cm)
		env["ANTHROPIC_DEFAULT_SONNET_MODEL_NAME"] = name
		env["CLAUDE_CODE_EFFORT_LEVEL"] = selection.Effort
	}
	if err := applyClaudeCodeProvider("deepseek", data); err != nil {
		return err
	}
	pruneBackups(BackupsDir(), backupsKeepPerLabel)
	return nil
}

// ApplyDeepSeekClaudeDesktop configures Claude Desktop's 3p gateway entry for
// DeepSeek with the chosen upstream model as the picker default.
func ApplyDeepSeekClaudeDesktop(model string) error {
	if !ValidDeepSeekUpstreamModel(model) {
		return fmt.Errorf("invalid DeepSeek model id %q (expected deepseek-*)", model)
	}
	app, err := ResolveHarness("desktop")
	if err != nil {
		return err
	}
	path, err := templatePathOrCreate(app.ID, "deepseek")
	if err != nil {
		return err
	}
	var data map[string]interface{}
	if _, err := toml.DecodeFile(path, &data); err != nil {
		return fmt.Errorf("load provider 'deepseek': %w", err)
	}
	data["model"] = model
	// Persist the selected model in the template so the quit -> re-apply ->
	// relaunch restart keeps the chosen picker default; ApplyProvider reloads
	// the template and would otherwise reset it to the preset order.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("write provider template: %w", err)
	}
	if err := toml.NewEncoder(f).Encode(data); err != nil {
		f.Close()
		return fmt.Errorf("write provider template: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("write provider template: %w", err)
	}
	if err := applyDesktopProvider("deepseek", data); err != nil {
		return err
	}
	pruneBackups(BackupsDir(), backupsKeepPerLabel)
	return nil
}

// ApplyClaudeProviderWithModel configures a Claude client with an
// Anthropic-native provider preset, selecting the given Claude-facing model
// alias. Claude Code writes the alias into its settings env; Claude Desktop
// records it as the picker default so the quit -> re-apply -> relaunch restart
// keeps the chosen model.
func ApplyClaudeProviderWithModel(app *HarnessInfo, providerName, model string) error {
	return ApplyClaudeProviderWithModelAndEffort(app, providerName, model, "")
}

// ApplyClaudeProviderWithModelAndEffort configures a Claude harness provider
// with a resolved model and effort.
func ApplyClaudeProviderWithModelAndEffort(app *HarnessInfo, providerName, model, effort string) error {
	preset, ok := KnownProviders()[providerName]
	if !ok || !preset.AnthropicNative {
		return fmt.Errorf("provider %q is not an Anthropic-native preset", providerName)
	}
	selection, err := ResolveHarnessSelection(HarnessClaude, providerName, model, effort)
	if err != nil {
		return err
	}
	cm := ClaudeModel{
		Alias:         selection.ClientModel,
		DisplayName:   selection.ClientModel,
		Upstream:      selection.UpstreamModel,
		ContextWindow: 0,
	}
	if harness, ok := HarnessProvider(HarnessClaude, providerName); ok {
		if mapped, found := harness.Models[selection.Model]; found {
			cm.DisplayName = mapped.DisplayName
			cm.ContextWindow = mapped.ContextWindow
		}
	}
	if err := EnsureClaudeProxyProvider(providerName); err != nil {
		return err
	}
	path, err := templatePathOrCreate(app.ID, providerName)
	if err != nil {
		return err
	}
	var data map[string]interface{}
	if _, err := toml.DecodeFile(path, &data); err != nil {
		return fmt.Errorf("load provider '%s': %w", providerName, err)
	}
	switch app.ID {
	case "claudecode":
		if env, ok := data["env"].(map[string]interface{}); ok {
			env["ANTHROPIC_DEFAULT_SONNET_MODEL"] = ClaudeCodeModelID(cm)
			env["ANTHROPIC_DEFAULT_SONNET_MODEL_NAME"] = cm.DisplayName
			env["CLAUDE_CODE_EFFORT_LEVEL"] = selection.Effort
		}
		if err := applyClaudeCodeProvider(providerName, data); err != nil {
			return err
		}
	case "desktop":
		data["model"] = cm.Alias
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
		if err != nil {
			return fmt.Errorf("write provider template: %w", err)
		}
		if err := toml.NewEncoder(f).Encode(data); err != nil {
			f.Close()
			return fmt.Errorf("write provider template: %w", err)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("write provider template: %w", err)
		}
		if err := applyDesktopProvider(providerName, data); err != nil {
			return err
		}
	default:
		return fmt.Errorf("no Claude apply path for app '%s'", app.ID)
	}
	pruneBackups(BackupsDir(), backupsKeepPerLabel)
	return nil
}

// ValidateModelOverride rejects --model for apps and providers that do not
// support a model override. Currently only Codex's native DeepSeek provider
// accepts one.
func ValidateModelOverride(app *HarnessInfo, providerName, model string) error {
	if app.ID != "codex" {
		return fmt.Errorf("--model is only supported for Codex")
	}
	if !IsNativeProvider(providerName) {
		return fmt.Errorf("--model is not supported for Codex provider %q", providerName)
	}
	if _, err := ResolveNativeModel(providerName, model); err != nil {
		return err
	}
	return nil
}

func applyClaudeCodeProvider(providerName string, data map[string]interface{}) error {
	path := ClaudeSettingsPath()
	existing := make(map[string]interface{})

	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &existing); err != nil {
			return fmt.Errorf("parse %s: %w (fix or delete the file manually)", path, err)
		}
	}

	if err := backup(path, providerName); err != nil {
		return fmt.Errorf("backup failed: %w", err)
	}
	if err := saveClaudeCodeNativeSnapshot(existing); err != nil {
		return fmt.Errorf("save native Claude Code snapshot: %w", err)
	}

	env, _ := existing["env"].(map[string]interface{})
	if env == nil {
		env = make(map[string]interface{})
	}
	if envRaw, ok := data["env"].(map[string]interface{}); ok {
		for _, key := range claudeCodeManagedEnvKeys {
			if value, exists := envRaw[key]; exists {
				env[key] = value
			}
		}
	}
	delete(env, "ANTHROPIC_AUTH_TOKEN")
	// Claude Code 2.1.211+: primaryApiKey in config.json no longer
	// bypasses OAuth. Only ANTHROPIC_API_KEY env var (source ==
	// "ANTHROPIC_API_KEY") makes AT() skip the OAuth path. Without
	// this, users with an expired Claude Pro/Max OAuth session get
	// "OAuth session expired" before any request reaches the proxy.
	// The proxy accepts any key from localhost (claudecode bypass),
	// so a dummy sk-ant-api03- value satisfies CC's local format
	// check while letting the proxy inject the real upstream token.
	env["ANTHROPIC_API_KEY"] = "sk-ant-api03-aix-proxy-managed"
	existing["env"] = env

	if model, ok := data["model"].(string); ok && model != "" {
		existing["model"] = model
	}

	out, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	return writePrivateFile(path, append(out, '\n'))
}

func applyDesktopProvider(providerName string, data map[string]interface{}) error {
	path := ClaudeDesktopConfigPath()
	existing := make(map[string]interface{})

	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &existing); err != nil {
			return fmt.Errorf("parse %s: %w (fix or delete the file manually)", path, err)
		}
	}

	if err := backup(path, providerName); err != nil {
		return fmt.Errorf("backup failed: %w", err)
	}

	mode, _ := data["deployment_mode"].(string)
	if mode == "3p" {
		if existing["deploymentMode"] != "3p" {
			if err := saveNativeDesktopSnap(existing); err != nil {
				return fmt.Errorf("save native snap: %w", err)
			}
		}
		existing["deploymentMode"] = "3p"
		if err := applyDesktop3pGateway(providerName, data); err != nil {
			return err
		}
	} else {
		restoreDesktopSnap(&existing)
	}

	out, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, append(out, '\n'), 0600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// applyDesktop3pGateway writes the Claude-3p data directory that activates
// third-party mode in current Claude Desktop builds. Newer builds ignore the
// legacy flat inferenceGateway* fields in the main claude_desktop_config.json;
// they only enter 3p mode when a config library entry under
// ~/Library/Application Support/Claude-3p/configLibrary/ carries the
// inferenceProvider (and required credential keys). aix writes both the
// deployment mode file and the applied config library entry.
func applyDesktop3pGateway(providerName string, data map[string]interface{}) error {
	if err := activateDesktop3pBackup(); err != nil {
		return err
	}
	proxyCfg, _ := LoadProxyConfig()
	listenAddr := proxyCfg.Listen
	if listenAddr == "" {
		listenAddr = "127.0.0.1:2026"
	}
	// The proxy configuration is authoritative. Provider templates can outlive
	// a gateway-key change, so using their cached value would make Claude
	// Desktop fail local authentication before a request reaches the upstream.
	gatewayKey := proxyCfg.GatewayKey
	if gatewayKey == "" {
		gatewayKey, _ = data["gateway_key"].(string)
	}
	if gatewayKey == "" {
		gatewayKey = DefaultGatewayAPIKey
	}
	baseURL := "http://" + listenAddr
	if providerName != "deepseek" {
		// Newer Anthropic-native providers route through an explicit
		// /<provider> prefix so the proxy can dispatch even when several
		// Anthropic-compatible sections exist. DeepSeek keeps the legacy
		// unprefixed base URL (it stays the first /anthropic route).
		baseURL += "/" + ClaudeProxyProviderID(providerName)
	}

	if err := writeDesktop3pAppConfig(); err != nil {
		return fmt.Errorf("write Claude-3p config: %w", err)
	}

	model, _ := data["model"].(string)
	entry := desktop3pGatewayEntry(baseURL, gatewayKey, providerName, model)
	meta, err := readDesktop3pMeta()
	if err != nil {
		return fmt.Errorf("read Claude-3p config library: %w", err)
	}

	id := desktop3pAppliedEntryID(meta)
	if id == "" {
		id = newConfigLibraryID()
		entries, _ := meta["entries"].([]interface{})
		meta["entries"] = append(entries, map[string]interface{}{
			"id":   id,
			"name": "AIX " + providerName,
		})
	} else if entries, ok := meta["entries"].([]interface{}); ok {
		for _, e := range entries {
			if em, ok := e.(map[string]interface{}); ok && em["id"] == id {
				em["name"] = "AIX " + providerName
				break
			}
		}
	}
	meta["appliedId"] = id

	if err := backup(ClaudeDesktop3pEntryPath(id), providerName); err != nil {
		return fmt.Errorf("backup Claude-3p entry: %w", err)
	}
	if err := writeJSON(ClaudeDesktop3pEntryPath(id), entry); err != nil {
		return fmt.Errorf("write Claude-3p config entry: %w", err)
	}
	if err := backup(ClaudeDesktop3pMetaPath(), providerName); err != nil {
		return fmt.Errorf("backup Claude-3p config library: %w", err)
	}
	if err := writeJSON(ClaudeDesktop3pMetaPath(), meta); err != nil {
		return fmt.Errorf("write Claude-3p config library: %w", err)
	}
	return nil
}

// writeDesktop3pAppConfig writes claude_desktop_config.json in the Claude-3p
// data directory with deploymentMode "3p". When the file does not exist yet,
// it is seeded from the main config so user preferences survive the switch;
// legacy flat gateway fields are dropped (current builds read them from the
// config library instead).
func writeDesktop3pAppConfig() error {
	p3p := ClaudeDesktop3pConfigPath()
	cfg := make(map[string]interface{})
	if raw, err := os.ReadFile(p3p); err == nil {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return fmt.Errorf("parse %s: %w (fix or delete the file manually)", p3p, err)
		}
	} else if os.IsNotExist(err) {
		if raw, err := os.ReadFile(ClaudeDesktopConfigPath()); err == nil {
			if err := json.Unmarshal(raw, &cfg); err != nil {
				return fmt.Errorf("parse %s: %w (fix or delete the file manually)", ClaudeDesktopConfigPath(), err)
			}
		}
	} else {
		return fmt.Errorf("read %s: %w", p3p, err)
	}
	for _, k := range desktopGatewayAuthFields {
		if k != "deploymentMode" {
			delete(cfg, k)
		}
	}
	cfg["deploymentMode"] = "3p"
	return writeJSON(p3p, cfg)
}

// desktop3pGatewayEntry returns the flat config library entry that activates
// the AIX gateway in Claude Desktop. The picker lists the provider's curated
// Claude-facing aliases (each rewritten by the proxy to its upstream model)
// with the selected model first as the default. Raw vendor/model slugs (e.g.
// OpenRouter's deepseek/*) are filtered out because the desktop model guard
// only accepts Anthropic-shaped ids; when such a slug is selected the default
// falls back to the provider's first curated alias.
//
// Aliases whose upstream context window is known to be at least 1M tokens
// advertise supports1m. The first/default model also sets prefer1m so a new
// conversation with no saved model selection starts on Claude Desktop's native
// 1M-context variant. Claude Desktop deliberately preserves an explicit user
// selection, so existing conversations are not rewritten by a provider switch.
func desktop3pGatewayEntry(baseURL, gatewayKey, providerID, selectedModel string) map[string]interface{} {
	entry := map[string]interface{}{
		"inferenceProvider":          "gateway",
		"inferenceGatewayBaseUrl":    baseURL,
		"inferenceGatewayApiKey":     gatewayKey,
		"inferenceGatewayAuthScheme": "x-api-key",
		"inferenceCredentialKind":    "static",
		"modelDiscoveryEnabled":      false,
		// Third-party deployments default this off. Keep it enabled so a
		// native Desktop session using Auto mode can cross the provider
		// boundary without a warning or a persisted downgrade to Manual.
		"autoModeEnabled": true,
	}
	harness, _ := HarnessProvider(HarnessClaude, providerID)
	selected, selectedErr := ResolveHarnessSelection(HarnessClaude, providerID, selectedModel, "")
	if selectedErr != nil || !strings.HasPrefix(selected.ClientModel, "claude-") {
		selected, _ = ResolveHarnessSelection(HarnessClaude, providerID, harness.DefaultModel, "")
	}
	modelIDs := sortedHarnessModelIDs(harness.Models)
	ordered := make([]HarnessModelSpec, 0, len(modelIDs))
	seenAliases := make(map[string]bool)
	if strings.HasPrefix(selected.ClientModel, "claude-") {
		if model, ok := harness.Models[selected.Model]; ok {
			ordered = append(ordered, model)
			seenAliases[model.ClientModel] = true
		}
	}
	// OpenCode Go exposes several MiniMax/Qwen upstreams through Claude-shaped
	// compatibility aliases. Keep them available for explicit CLI selection
	// and routing, but the default Desktop picker advertises only the three
	// DeepSeek V4 models verified against its Anthropic Messages endpoint.
	for _, modelID := range modelIDs {
		model := harness.Models[modelID]
		if !strings.HasPrefix(model.ClientModel, "claude-") || seenAliases[model.ClientModel] {
			continue
		}
		if providerID == "opencode-go" && !strings.HasPrefix(model.UpstreamModel, "deepseek-v4-") {
			continue
		}
		ordered = append(ordered, model)
		seenAliases[model.ClientModel] = true
	}
	var models []interface{}
	for i, model := range ordered {
		displayName := model.DisplayName
		if providerID == "deepseek" {
			displayName = model.UpstreamModel
		}
		standard := map[string]interface{}{
			"name":          model.ClientModel,
			"labelOverride": displayName,
		}
		if model.ContextWindow >= oneMillionContext {
			standard["supports1m"] = true
			if i == 0 {
				standard["prefer1m"] = true
			}
		}
		if i == 0 {
			// Claude Desktop's default surface is the sonnet tier. Pinning the
			// first entry to that tier makes its Default badge agree with the
			// configured first/default model instead of the first sonnet-shaped
			// fallback later in the list.
			standard["anthropicFamilyTier"] = "sonnet"
			standard["isFamilyDefault"] = true
		}
		models = append(models, standard)
	}
	if len(models) > 0 {
		entry["inferenceModels"] = models
	}
	return entry
}

// readDesktop3pMeta returns the config library index, defaulting to an empty
// library when the file does not exist.
func readDesktop3pMeta() (map[string]interface{}, error) {
	meta := map[string]interface{}{
		"appliedId": "",
		"entries":   []interface{}{},
	}
	raw, err := os.ReadFile(ClaudeDesktop3pMetaPath())
	if os.IsNotExist(err) {
		return meta, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, fmt.Errorf("parse %s: %w", ClaudeDesktop3pMetaPath(), err)
	}
	return meta, nil
}

// desktop3pAppliedEntryID returns the currently applied config library id when
// it points at an AIX-managed gateway entry; otherwise "" so a new entry is
// created without clobbering the user's own configuration.
func desktop3pAppliedEntryID(meta map[string]interface{}) string {
	id, _ := meta["appliedId"].(string)
	if id == "" {
		return ""
	}
	raw, err := os.ReadFile(ClaudeDesktop3pEntryPath(id))
	if err != nil {
		return ""
	}
	var entry map[string]interface{}
	if json.Unmarshal(raw, &entry) != nil {
		return ""
	}
	provider, _ := entry["inferenceProvider"].(string)
	if provider != "gateway" {
		return ""
	}
	return id
}

// desktop3pAppliedEntryIDFromDisk returns the applied AIX gateway config
// library id when the Claude-3p library is present on disk.
func desktop3pAppliedEntryIDFromDisk() (string, bool) {
	meta, err := readDesktop3pMeta()
	if err != nil {
		return "", false
	}
	id := desktop3pAppliedEntryID(meta)
	return id, id != ""
}

// newConfigLibraryID returns a random UUID v4 matching the app's config
// library id format ([a-f0-9-]{36}).
func newConfigLibraryID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failure is unrecoverable in practice; fall back to a
		// zero id so the app still accepts the entry format.
		return "00000000-0000-4000-8000-000000000000"
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// writeJSON marshals v as indented JSON with a trailing newline, creating
// parent directories as needed.
func writeJSON(path string, v interface{}) error {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0600)
}

func applyCodexProvider(providerName string, data map[string]interface{}) error {
	if !IsNativeProvider(providerName) {
		return fmt.Errorf("provider %q is not a Codex Responses provider; only native Responses API providers are supported for Codex", providerName)
	}
	model, _ := data["model"].(string)
	effort, _ := data["effort"].(string)
	selection, err := ResolveHarnessSelection(HarnessCodex, providerName, model, effort)
	if err != nil {
		return err
	}
	spec, _ := NativeProvider(providerName)
	key, _ := NativeProviderAPIKey(providerName)
	if key == "" {
		return fmt.Errorf("%s API key not found; set $%s or add an auth_token for %q to proxy.toml", spec.Name, spec.EnvKey, spec.Name)
	}
	return ConfigureCodexProxyWithEffort(providerName, selection.ClientModel, selection.Effort, key)
}

func backup(path, label string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	base := filepath.Base(path)
	ts := time.Now().Format("20060102-150405.000")
	bp := filepath.Join(BackupsDir(), fmt.Sprintf("%s.%s.%s.bak", base, label, ts))
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := os.WriteFile(bp, data, 0600); err != nil {
		return err
	}
	return nil
}

// BackupFile snapshots a config file into the backups directory, used by
// write paths outside the apply flow (e.g. the web dashboard).
func BackupFile(path, label string) error {
	return backup(path, label)
}

// PruneBackups prunes old backups per label group, keeping the most recent
// backupsKeepPerLabel for each.
func PruneBackups() {
	pruneBackups(BackupsDir(), backupsKeepPerLabel)
}

const backupsKeepPerLabel = 20

// pruneBackups keeps the most recent keepPerLabel backups per label group and
// removes older ones. Groups share everything before the timestamp suffix
// (e.g. "config.toml.codex-deepseek").
func pruneBackups(dir string, keepPerLabel int) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.bak"))
	if err != nil || len(matches) <= keepPerLabel {
		return
	}
	groups := make(map[string][]string)
	for _, m := range matches {
		if group := backupLabelGroup(filepath.Base(m)); group != "" {
			groups[group] = append(groups[group], filepath.Base(m))
		}
	}
	for _, names := range groups {
		if len(names) <= keepPerLabel {
			continue
		}
		sort.Strings(names) // fixed-width timestamp suffix sorts newest last
		for _, name := range names[:len(names)-keepPerLabel] {
			os.Remove(filepath.Join(dir, name))
		}
	}
}

// backupLabelGroup returns the group key for a backup name: everything before
// the trailing "<timestamp>.<ms>.bak" suffix.
func backupLabelGroup(name string) string {
	trimmed := strings.TrimSuffix(name, ".bak")
	if idx := strings.LastIndex(trimmed, "."); idx >= 0 {
		trimmed = trimmed[:idx]
	}
	if idx := strings.LastIndex(trimmed, "."); idx >= 0 {
		return trimmed[:idx]
	}
	return ""
}

func applyExcalidrawProvider(providerName string, data map[string]interface{}) error {
	model, _ := data["model"].(string)
	if model == "" {
		return fmt.Errorf("model not specified in provider config")
	}

	vaults, err := findExcalidrawVaults()
	if err != nil {
		return fmt.Errorf("find vaults: %w", err)
	}
	if len(vaults) == 0 {
		return fmt.Errorf("no Obsidian vaults with Excalidraw plugin found")
	}

	proxyCfg, _ := LoadProxyConfig()
	listenAddr := proxyCfg.Listen
	if listenAddr == "" {
		listenAddr = "127.0.0.1:2026"
	}
	baseURL := "http://" + listenAddr + "/v1"
	apiKey := proxyCfg.GatewayKey
	if apiKey == "" {
		apiKey = DefaultGatewayAPIKey
	}

	var errs []string
	for _, vaultPath := range vaults {
		pluginDataPath := filepath.Join(vaultPath, ".obsidian", "plugins", "obsidian-excalidraw-plugin", "data.json")
		if err := updateExcalidrawPluginData(pluginDataPath, model, baseURL, apiKey, providerName); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", vaultPath, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("failed to update some vaults:\n%s", strings.Join(errs, "\n"))
	}
	return nil
}

type obsidianConfig struct {
	Vaults map[string]struct {
		Path string `json:"path"`
		Ts   int64  `json:"ts"`
	} `json:"vaults"`
}

func findExcalidrawVaults() ([]string, error) {
	configPath := ObsidianConfigPath()
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	var cfg obsidianConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse obsidian.json: %w", err)
	}
	var vaults []string
	for _, v := range cfg.Vaults {
		pluginData := filepath.Join(v.Path, ".obsidian", "plugins", "obsidian-excalidraw-plugin", "data.json")
		if _, err := os.Stat(pluginData); err == nil {
			vaults = append(vaults, v.Path)
		}
	}
	return vaults, nil
}

func updateExcalidrawPluginData(path, model, baseURL, apiKey, providerName string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var pluginData map[string]interface{}
	if err := json.Unmarshal(raw, &pluginData); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}

	if err := backup(path, providerName); err != nil {
		return fmt.Errorf("backup failed: %w", err)
	}

	profileName := "aix-proxy"
	modelKey := model

	pluginData["aiEnabled"] = true
	pluginData["aiVerboseLogging"] = false

	if pluginData["aiProviderProfiles"] == nil {
		pluginData["aiProviderProfiles"] = map[string]interface{}{}
	}
	profiles, _ := pluginData["aiProviderProfiles"].(map[string]interface{})
	if profiles == nil {
		profiles = map[string]interface{}{}
	}
	profiles[profileName] = map[string]interface{}{
		"provider": "openai-compatible",
		"apiKey":   apiKey,
		"baseURL":  baseURL,
	}
	pluginData["aiProviderProfiles"] = profiles

	if pluginData["aiTextModelConfigs"] == nil {
		pluginData["aiTextModelConfigs"] = map[string]interface{}{}
	}
	textModels, _ := pluginData["aiTextModelConfigs"].(map[string]interface{})
	if textModels == nil {
		textModels = map[string]interface{}{}
	}
	textModels[modelKey] = map[string]interface{}{
		"providerId":        profileName,
		"model":             model,
		"endpoint":          "",
		"multimodalSupport": true,
	}
	pluginData["aiTextModelConfigs"] = textModels

	pluginData["aiDefaultTextModel"] = modelKey

	out, err := json.MarshalIndent(pluginData, "", "\t")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	return os.WriteFile(path, out, 0600)
}
