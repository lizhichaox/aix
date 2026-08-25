package internal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyDeepSeekClaudeCode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureProviderTemplate("claudecode", "deepseek"); err != nil {
		t.Fatalf("EnsureProviderTemplate: %v", err)
	}
	if err := ApplyDeepSeekClaudeCode(DeepSeekV4FlashModel); err != nil {
		t.Fatalf("ApplyDeepSeekClaudeCode: %v", err)
	}
	raw, err := os.ReadFile(ClaudeSettingsPath())
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var s map[string]interface{}
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	env, _ := s["env"].(map[string]interface{})
	if env == nil {
		t.Fatal("settings env block missing")
	}
	wantModel := ClaudeCodeDeepSeekModel + "[1m]"
	if got := env["ANTHROPIC_DEFAULT_SONNET_MODEL"]; got != wantModel {
		t.Errorf("ANTHROPIC_DEFAULT_SONNET_MODEL = %v, want %q", got, wantModel)
	}
	if got := env["ANTHROPIC_DEFAULT_SONNET_MODEL_NAME"]; got != ClaudeCodeDeepSeekModelName {
		t.Errorf("ANTHROPIC_DEFAULT_SONNET_MODEL_NAME = %v, want %q", got, ClaudeCodeDeepSeekModelName)
	}
	if got := env["ANTHROPIC_BASE_URL"]; got != "http://127.0.0.1:2026" {
		t.Errorf("ANTHROPIC_BASE_URL = %v", got)
	}
	if key, _ := env["ANTHROPIC_API_KEY"].(string); key == "" {
		t.Error("ANTHROPIC_API_KEY must be set (proxy-managed dummy key)")
	}
	if err := ApplyDeepSeekClaudeCode("gpt-5.5"); err == nil {
		t.Error("ApplyDeepSeekClaudeCode should reject unknown models")
	}
}

func TestApplyDynamicDeepSeekClaudeCodeUses1M(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureProviderTemplate("claudecode", "deepseek"); err != nil {
		t.Fatalf("EnsureProviderTemplate: %v", err)
	}
	const model = "deepseek-v4-flash-vision-exp"
	if err := ApplyDeepSeekClaudeCode(model); err != nil {
		t.Fatalf("ApplyDeepSeekClaudeCode: %v", err)
	}
	var settings map[string]interface{}
	if err := readJSONInto(ClaudeSettingsPath(), &settings); err != nil {
		t.Fatal(err)
	}
	env, _ := settings["env"].(map[string]interface{})
	alias, _ := ClaudeDeepSeekAlias(model)
	if got := env["ANTHROPIC_DEFAULT_SONNET_MODEL"]; got != alias+"[1m]" {
		t.Errorf("dynamic model = %v, want %q", got, alias+"[1m]")
	}
}

func TestApplyDesktop3pGatewayWritesConfigLibrary(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := EnsureDirs(); err != nil {
		t.Fatal(err)
	}

	// Seed a main config with a preference so the 3p config inherits it.
	mainCfg := map[string]interface{}{
		"preferences": map[string]interface{}{
			"sidebarMode": "chat",
		},
	}
	if err := writeJSON(ClaudeDesktopConfigPath(), mainCfg); err != nil {
		t.Fatal(err)
	}

	// Give the proxy a deepseek-anthropic provider so the gateway entry labels
	// models with their real DeepSeek backend.
	if err := os.WriteFile(ProxyConfigPath(), []byte(`gateway_key = "current-gateway"

[providers.deepseek-anthropic]
upstream = "https://api.deepseek.com/anthropic"
auth_token = "test"
[providers.deepseek-anthropic.models]
claude-opus-5 = "deepseek-v4-flash"
claude-fable-5 = "deepseek-v4-pro"
`), 0600); err != nil {
		t.Fatal(err)
	}

	if err := applyDesktopProvider("deepseek", map[string]interface{}{
		"deployment_mode": "3p",
		"gateway_key":     "aix-gateway",
		"model":           DeepSeekV4FlashModel,
	}); err != nil {
		t.Fatalf("applyDesktopProvider: %v", err)
	}

	// The 3p config file must carry deploymentMode and seeded preferences.
	var p3p map[string]interface{}
	if err := readJSONInto(ClaudeDesktop3pConfigPath(), &p3p); err != nil {
		t.Fatalf("read 3p config: %v", err)
	}
	if got := p3p["deploymentMode"]; got != "3p" {
		t.Errorf("3p deploymentMode = %v, want 3p", got)
	}
	if _, ok := p3p["preferences"].(map[string]interface{}); !ok {
		t.Error("3p config should inherit preferences from the main config")
	}

	// The config library must have an applied gateway entry.
	var meta map[string]interface{}
	if err := readJSONInto(ClaudeDesktop3pMetaPath(), &meta); err != nil {
		t.Fatalf("read config library meta: %v", err)
	}
	appliedID, _ := meta["appliedId"].(string)
	if appliedID == "" {
		t.Fatal("config library appliedId missing")
	}
	var entry map[string]interface{}
	if err := readJSONInto(ClaudeDesktop3pEntryPath(appliedID), &entry); err != nil {
		t.Fatalf("read config entry: %v", err)
	}
	if got := entry["inferenceProvider"]; got != "gateway" {
		t.Errorf("inferenceProvider = %v, want gateway", got)
	}
	if got := entry["inferenceGatewayBaseUrl"]; got != "http://127.0.0.1:2026" {
		t.Errorf("inferenceGatewayBaseUrl = %v", got)
	}
	if got := entry["inferenceGatewayAuthScheme"]; got != "x-api-key" {
		t.Errorf("inferenceGatewayAuthScheme = %v, want x-api-key", got)
	}
	if got := entry["inferenceCredentialKind"]; got != "static" {
		t.Errorf("inferenceCredentialKind = %v, want static", got)
	}
	if got := entry["inferenceGatewayApiKey"]; got != "current-gateway" {
		t.Errorf("inferenceGatewayApiKey = %v, want current proxy gateway key", got)
	}
	models, _ := entry["inferenceModels"].([]interface{})
	if len(models) == 0 {
		t.Fatal("inferenceModels missing")
	}
	for _, m := range models {
		mm, _ := m.(map[string]interface{})
		name, _ := mm["name"].(string)
		if strings.HasPrefix(name, "deepseek-") {
			t.Errorf("model %q must use an Anthropic-shaped alias the app accepts", name)
		}
		if label, _ := mm["labelOverride"].(string); !strings.HasPrefix(label, "deepseek-") {
			t.Errorf("labelOverride %q should show the DeepSeek upstream model", label)
		}
	}
	if first, ok := models[0].(map[string]interface{}); ok {
		want := ClaudeCodeDeepSeekModel
		if first["name"] != want {
			t.Errorf("default picker model = %v, want %q", first["name"], want)
		}
		if first["supports1m"] != true || first["prefer1m"] != true {
			t.Errorf("default picker model %q should prefer its native 1M variant: %#v", first["name"], first)
		}
	}

	// Switching to pro must put Claude Fable 5 first in the picker.
	if err := applyDesktopProvider("deepseek", map[string]interface{}{
		"deployment_mode": "3p",
		"gateway_key":     "aix-gateway",
		"model":           DeepSeekV4ProModel,
	}); err != nil {
		t.Fatalf("applyDesktopProvider(pro): %v", err)
	}
	if err := readJSONInto(ClaudeDesktop3pEntryPath(appliedID), &entry); err != nil {
		t.Fatalf("re-read config entry: %v", err)
	}
	models, _ = entry["inferenceModels"].([]interface{})
	if len(models) == 0 {
		t.Fatal("inferenceModels missing after pro switch")
	}
	if first, ok := models[0].(map[string]interface{}); ok {
		want := ClaudeCodeDeepSeekProModel
		if first["name"] != want {
			t.Errorf("default picker model = %v, want %q", first["name"], want)
		}
		if first["supports1m"] != true || first["prefer1m"] != true {
			t.Errorf("default picker model %q should prefer its native 1M variant: %#v", first["name"], first)
		}
	}

	// StatusMode must report the gateway through the config library.
	app, err := ResolveApp("desktop")
	if err != nil {
		t.Fatal(err)
	}
	mode, _, detail := app.StatusMode()
	if mode != "gateway" {
		t.Errorf("StatusMode = %q, want gateway", mode)
	}
	if !strings.Contains(detail, "127.0.0.1:2026") {
		t.Errorf("StatusMode detail = %q, want gateway base URL", detail)
	}

	// Restore must remove the 3p data dir and reset the main config.
	if err := RestoreNative(app); err != nil {
		t.Fatalf("RestoreNative: %v", err)
	}
	if _, err := os.Stat(ClaudeDesktop3pDir()); !os.IsNotExist(err) {
		t.Error("Claude-3p data dir should be removed after restore")
	}
	var restored map[string]interface{}
	if err := readJSONInto(ClaudeDesktopConfigPath(), &restored); err != nil {
		t.Fatalf("read restored config: %v", err)
	}
	if got := restored["deploymentMode"]; got != "1p" {
		t.Errorf("deploymentMode after restore = %v, want 1p", got)
	}
	if mode, _, _ := app.StatusMode(); mode != "native" {
		t.Errorf("StatusMode after restore = %q, want native", mode)
	}
}

func TestRestoreDesktopNativeWithoutSnapshotSets1p(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(ClaudeDesktopConfigPath(), map[string]interface{}{
		"deploymentMode":             "3p",
		"inferenceGatewayBaseUrl":    "http://127.0.0.1:2026",
		"inferenceGatewayApiKey":     "aix-gateway",
		"inferenceGatewayAuthScheme": "x-api-key",
		"preferences": map[string]interface{}{
			"sidebarMode": "code",
		},
	}); err != nil {
		t.Fatal(err)
	}

	app, err := ResolveApp("desktop")
	if err != nil {
		t.Fatal(err)
	}
	if err := RestoreNative(app); err != nil {
		t.Fatalf("RestoreNative: %v", err)
	}

	var restored map[string]interface{}
	if err := readJSONInto(ClaudeDesktopConfigPath(), &restored); err != nil {
		t.Fatal(err)
	}
	if got := restored["deploymentMode"]; got != "1p" {
		t.Errorf("deploymentMode = %v, want 1p", got)
	}
	for _, key := range []string{"inferenceGatewayBaseUrl", "inferenceGatewayApiKey", "inferenceGatewayAuthScheme"} {
		if _, ok := restored[key]; ok {
			t.Errorf("gateway field %q should be removed", key)
		}
	}
	if _, ok := restored["preferences"]; !ok {
		t.Error("preferences should be preserved")
	}
}

func TestRemoveDesktop3pDirArchivesStaleBackup(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := EnsureDirs(); err != nil {
		t.Fatal(err)
	}

	// Simulate a restore cycle that left a stale backup slot behind while a
	// newer switch recreated the active third-party data directory.
	active := ClaudeDesktop3pDir()
	bak := active + ".bak"
	if err := os.MkdirAll(active, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bak, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bak, "snapshot.json"), []byte(`{"cycle":"old"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(active, "state.json"), []byte(`{"cycle":"current"}`), 0600); err != nil {
		t.Fatal(err)
	}

	if err := removeDesktop3pDir(); err != nil {
		t.Fatalf("removeDesktop3pDir with stale backup: %v", err)
	}

	// The current data must claim the canonical backup slot.
	if raw, err := os.ReadFile(filepath.Join(bak, "state.json")); err != nil || string(raw) != `{"cycle":"current"}` {
		t.Errorf("current data not preserved in canonical backup: %q, %v", raw, err)
	}
	if _, err := os.Stat(active); !os.IsNotExist(err) {
		t.Error("active third-party data directory should be gone after restore")
	}

	// The stale snapshot must be archived, not deleted, and restorable.
	matches, err := filepath.Glob(bak + ".*")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatal("stale backup was not archived")
	}
	archived := matches[0]
	if raw, err := os.ReadFile(filepath.Join(archived, "snapshot.json")); err != nil || string(raw) != `{"cycle":"old"}` {
		t.Errorf("stale snapshot not preserved in archive: %q, %v", raw, err)
	}
}

func TestDesktop3pGatewayEntryLabelsDynamicDeepSeekModel(t *testing.T) {
	const upstream = "deepseek-v4-flash-vision-exp"
	alias, _ := ClaudeDeepSeekAlias(upstream)
	entry := desktop3pGatewayEntry("http://127.0.0.1:2026", "key", "deepseek", upstream)
	models, _ := entry["inferenceModels"].([]interface{})
	if len(models) == 0 {
		t.Fatal("dynamic model missing from picker")
	}
	first, _ := models[0].(map[string]interface{})
	if first["name"] != alias {
		t.Errorf("dynamic picker alias = %v, want %q", first["name"], alias)
	}
	if first["labelOverride"] != upstream {
		t.Errorf("dynamic picker label = %v, want %q", first["labelOverride"], upstream)
	}
	if first["supports1m"] != true || first["prefer1m"] != true {
		t.Errorf("dynamic picker default should prefer its native 1M variant: %#v", first)
	}
	if first["anthropicFamilyTier"] != "sonnet" || first["isFamilyDefault"] != true {
		t.Errorf("first picker model must own the default sonnet tier: %#v", first)
	}
	if len(models) != 3 {
		t.Fatalf("DeepSeek picker models = %d, want 3 native 1M-capable rows", len(models))
	}
	wantLabels := []string{upstream, DeepSeekV4FlashModel, DeepSeekV4ProModel}
	for i, want := range wantLabels {
		model, _ := models[i].(map[string]interface{})
		if got := model["labelOverride"]; got != want {
			t.Errorf("picker label %d = %v, want %q", i, got, want)
		}
	}
}

func TestDesktopOpenCodeGoPickerPrefersNative1MDefault(t *testing.T) {
	entry := desktop3pGatewayEntry("http://127.0.0.1:2026/opencode-go", "key", "opencode-go", "")
	models, _ := entry["inferenceModels"].([]interface{})
	if len(models) != 3 {
		t.Fatalf("default OpenCode Go picker models = %d, want three native 1M-capable DeepSeek models", len(models))
	}
	first, _ := models[0].(map[string]interface{})
	if first["labelOverride"] != DefaultClaudeUpstreamModel {
		t.Errorf("first label = %v, want DeepSeek default label", first["labelOverride"])
	}
	if first["supports1m"] != true || first["prefer1m"] != true {
		t.Errorf("default OpenCode Go model should prefer its native 1M variant: %#v", first)
	}
	for _, raw := range models {
		model, _ := raw.(map[string]interface{})
		label, _ := model["labelOverride"].(string)
		if strings.HasPrefix(label, "Claude ") {
			t.Errorf("default OpenCode Go picker leaked compatibility model %q", label)
		}
	}

	entry = desktop3pGatewayEntry("http://127.0.0.1:2026/opencode-go", "key", "opencode-go", "minimax-m2.7")
	models, _ = entry["inferenceModels"].([]interface{})
	if len(models) != 4 {
		t.Fatalf("explicit OpenCode Go picker models = %d, want selected model plus three DeepSeek rows", len(models))
	}
	first, _ = models[0].(map[string]interface{})
	if first["labelOverride"] != "Claude Sonnet 4.6" {
		t.Errorf("explicit picker label = %v, want selected compatibility model", first["labelOverride"])
	}
}

func readJSONInto(path string, dest interface{}) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, dest)
}

func TestBackup_CreatesBackup(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	os.MkdirAll(BackupsDir(), 0755)

	// Snapshot existing .bak files so we only clean up the one we create.
	existing := make(map[string]bool)
	if entries, err := os.ReadDir(BackupsDir()); err == nil {
		for _, e := range entries {
			if filepath.Ext(e.Name()) == ".bak" {
				existing[e.Name()] = true
			}
		}
	}

	dir := t.TempDir()
	src := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(src, []byte(`{"key":"value"}`), 0600); err != nil {
		t.Fatal(err)
	}

	if err := backup(src, "testprovider"); err != nil {
		t.Fatalf("backup failed: %v", err)
	}

	// Find the newly created backup file (not in the snapshot).
	entries, err := os.ReadDir(BackupsDir())
	if err != nil {
		t.Fatal(err)
	}
	var newBak string
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".bak" && !existing[e.Name()] {
			newBak = filepath.Join(BackupsDir(), e.Name())
			break
		}
	}
	if newBak == "" {
		t.Error("no new backup file found")
	} else {
		os.Remove(newBak)
	}
}

func TestBackup_SourceNotExist(t *testing.T) {
	err := backup("/nonexistent/path/config.json", "test")
	if err != nil {
		t.Errorf("backup should not error on missing source: %v", err)
	}
}

func TestBackup_PreservesContent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	os.MkdirAll(BackupsDir(), 0755)

	// Snapshot existing .bak files to avoid picking up pre-existing ones.
	existing := make(map[string]bool)
	if entries, err := os.ReadDir(BackupsDir()); err == nil {
		for _, e := range entries {
			if filepath.Ext(e.Name()) == ".bak" {
				existing[e.Name()] = true
			}
		}
	}

	dir := t.TempDir()
	src := filepath.Join(dir, "config.toml")
	original := []byte("model = \"gpt-5.5\"\n")
	if err := os.WriteFile(src, original, 0600); err != nil {
		t.Fatal(err)
	}

	if err := backup(src, "deepseek"); err != nil {
		t.Fatalf("backup failed: %v", err)
	}

	entries, _ := os.ReadDir(BackupsDir())
	var bakPath string
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".bak" && !existing[e.Name()] {
			bakPath = filepath.Join(BackupsDir(), e.Name())
			break
		}
	}
	if bakPath == "" {
		t.Fatal("new backup file not found")
	}
	defer os.Remove(bakPath)

	data, err := os.ReadFile(bakPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(original) {
		t.Errorf("backup content = %q, want %q", data, original)
	}
}

func TestProviderPath_Safety(t *testing.T) {
	cases := []struct {
		appID, provider, wantEmpty string
	}{
		{"claudecode", "deepseek", ""},
		{"desktop", "kimi", ""},
		{"claudecode", "../escape", "should reject"},
		{"claudecode", "sub/dir", "should reject"},
		{"claudecode", "a\\b", "should reject"},
		{"codex", "qwen", ""},
	}
	for _, tc := range cases {
		got := ProviderPath(tc.appID, tc.provider)
		if tc.wantEmpty != "" && got != "" {
			t.Errorf("ProviderPath(%q, %q) = %q, want empty (%s)", tc.appID, tc.provider, got, tc.wantEmpty)
		}
		if tc.wantEmpty == "" && got == "" {
			t.Errorf("ProviderPath(%q, %q) = empty, want non-empty", tc.appID, tc.provider)
		}
	}
}

func TestProviderPath_ValidStructure(t *testing.T) {
	path := ProviderPath("claudecode", "deepseek")
	if path == "" {
		t.Fatal("path should not be empty")
	}
	if filepath.Base(path) != "deepseek.toml" {
		t.Errorf("base = %q, want deepseek.toml", filepath.Base(path))
	}
}

func TestValidateModelOverride(t *testing.T) {
	codex := &AppInfo{ID: "codex"}
	claude := &AppInfo{ID: "claudecode"}
	if err := ValidateModelOverride(claude, "deepseek", DeepSeekV4FlashModel); err == nil {
		t.Error("model override should be rejected for non-Codex apps")
	}
	if err := ValidateModelOverride(codex, "kimi", "kimi-k2.7-code"); err == nil {
		t.Error("model override should be rejected for proxy-mode Codex providers")
	}
	if err := ValidateModelOverride(codex, "deepseek", "gpt-5.5"); err == nil {
		t.Error("model override should reject unsupported DeepSeek models")
	}
	if err := ValidateModelOverride(codex, "deepseek", DeepSeekV4FlashModel); err != nil {
		t.Errorf("valid model override rejected: %v", err)
	}
}

func TestBackupLabelGroup(t *testing.T) {
	cases := map[string]string{
		"config.toml.codex-deepseek.20260805-150155.670.bak":        "config.toml.codex-deepseek",
		"claude_desktop_config.json.native.20260701-000000.000.bak": "claude_desktop_config.json.native",
		"settings.json.deepseek.20260801-120000.123.bak":            "settings.json.deepseek",
		"not-a-backup": "",
	}
	for name, want := range cases {
		if got := backupLabelGroup(name); got != want {
			t.Errorf("backupLabelGroup(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestPruneBackupsKeepsNewestPerLabel(t *testing.T) {
	dir := t.TempDir()
	names := []string{
		"config.toml.codex-deepseek.20260801-000000.000.bak",
		"config.toml.codex-deepseek.20260802-000000.000.bak",
		"config.toml.codex-deepseek.20260803-000000.000.bak",
		"config.toml.codex-deepseek.20260804-000000.000.bak",
		"config.toml.codex-deepseek.20260805-000000.000.bak",
		"models.json.codex-deepseek-models.20260801-000000.000.bak",
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}

	pruneBackups(dir, 2)

	left, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range left {
		got = append(got, e.Name())
	}
	want := map[string]bool{
		"config.toml.codex-deepseek.20260804-000000.000.bak":        true,
		"config.toml.codex-deepseek.20260805-000000.000.bak":        true,
		"models.json.codex-deepseek-models.20260801-000000.000.bak": true,
	}
	if len(got) != len(want) {
		t.Fatalf("files after prune = %v, want %v", got, want)
	}
	for _, n := range got {
		if !want[n] {
			t.Errorf("unexpected file after prune: %s", n)
		}
	}
}

func TestApplyClaudeProviderWithModelOpenRouter(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENROUTER_API_KEY", "sk-or-test")
	if err := EnsureDirs(); err != nil {
		t.Fatal(err)
	}

	desktop, err := ResolveApp("desktop")
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyClaudeProviderWithModel(desktop, "openrouter", "claude-opus-5"); err != nil {
		t.Fatalf("apply desktop: %v", err)
	}

	// Desktop must point at the explicit /openrouter prefix so the proxy
	// dispatches to the right Anthropic-compatible section.
	var meta map[string]interface{}
	if err := readJSONInto(ClaudeDesktop3pMetaPath(), &meta); err != nil {
		t.Fatal(err)
	}
	appliedID, _ := meta["appliedId"].(string)
	if appliedID == "" {
		t.Fatal("config library appliedId missing")
	}
	var entry map[string]interface{}
	if err := readJSONInto(ClaudeDesktop3pEntryPath(appliedID), &entry); err != nil {
		t.Fatal(err)
	}
	if got := entry["inferenceGatewayBaseUrl"]; got != "http://127.0.0.1:2026/openrouter" {
		t.Errorf("inferenceGatewayBaseUrl = %v, want /openrouter prefix", got)
	}
	models, _ := entry["inferenceModels"].([]interface{})
	if len(models) == 0 {
		t.Fatal("inferenceModels missing")
	}
	if first, ok := models[0].(map[string]interface{}); ok {
		if first["name"] != "claude-opus-5" {
			t.Errorf("default picker model = %v, want claude-opus-5", first["name"])
		}
		if first["labelOverride"] != "Claude Opus 5" {
			t.Errorf("labelOverride = %v, want Claude Opus 5", first["labelOverride"])
		}
		if _, ok := first["supports1m"]; ok {
			t.Errorf("openrouter model %q must not advertise 1M context (upstream window unknown)", first["name"])
		}
	}

	// The proxy section must exist with the alias -> upstream mapping.
	cfg, err := LoadProxyConfig()
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Providers["openrouter"]
	if p == nil {
		t.Fatal("openrouter proxy provider missing")
	}
	if got := p.Models["claude-opus-5"]; got != "anthropic/claude-opus-5" {
		t.Errorf("claude-opus-5 mapping = %q, want anthropic/claude-opus-5", got)
	}

	cc, err := ResolveApp("claudecode")
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyClaudeProviderWithModelAndEffort(cc, "openrouter", "claude-sonnet-4-6", "xhigh"); err != nil {
		t.Fatalf("apply claudecode: %v", err)
	}
	raw, err := os.ReadFile(ClaudeSettingsPath())
	if err != nil {
		t.Fatal(err)
	}
	var s map[string]interface{}
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	env, _ := s["env"].(map[string]interface{})
	if env == nil {
		t.Fatal("settings env block missing")
	}
	if got := env["ANTHROPIC_BASE_URL"]; got != "http://127.0.0.1:2026/openrouter" {
		t.Errorf("ANTHROPIC_BASE_URL = %v, want /openrouter prefix", got)
	}
	if got := env["ANTHROPIC_DEFAULT_SONNET_MODEL"]; got != "claude-sonnet-4-6" {
		t.Errorf("ANTHROPIC_DEFAULT_SONNET_MODEL = %v, want claude-sonnet-4-6", got)
	}
	if got := env["ANTHROPIC_DEFAULT_SONNET_MODEL_NAME"]; got != "Claude Sonnet 4.6" {
		t.Errorf("ANTHROPIC_DEFAULT_SONNET_MODEL_NAME = %v, want Claude Sonnet 4.6", got)
	}
	if got := env["CLAUDE_CODE_EFFORT_LEVEL"]; got != "xhigh" {
		t.Errorf("CLAUDE_CODE_EFFORT_LEVEL = %v, want xhigh", got)
	}
}

func TestDesktopOpenRouterRawSlugFiltered(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENROUTER_API_KEY", "sk-or-test")
	if err := EnsureDirs(); err != nil {
		t.Fatal(err)
	}

	desktop, err := ResolveApp("desktop")
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyClaudeProviderWithModel(desktop, "openrouter", "deepseek/deepseek-v4-flash"); err != nil {
		t.Fatalf("apply desktop: %v", err)
	}

	var meta map[string]interface{}
	if err := readJSONInto(ClaudeDesktop3pMetaPath(), &meta); err != nil {
		t.Fatal(err)
	}
	appliedID, _ := meta["appliedId"].(string)
	if appliedID == "" {
		t.Fatal("config library appliedId missing")
	}
	var entry map[string]interface{}
	if err := readJSONInto(ClaudeDesktop3pEntryPath(appliedID), &entry); err != nil {
		t.Fatal(err)
	}
	models, _ := entry["inferenceModels"].([]interface{})
	if len(models) == 0 {
		t.Fatal("inferenceModels missing")
	}
	for _, m := range models {
		mm, _ := m.(map[string]interface{})
		name, _ := mm["name"].(string)
		if !strings.HasPrefix(name, "claude-") {
			t.Errorf("desktop inference model %q is not Anthropic-shaped", name)
		}
	}
	if first, ok := models[0].(map[string]interface{}); ok {
		defaultModel, err := ResolveHarnessSelection(HarnessClaude, "openrouter", "", "")
		if err != nil {
			t.Fatal(err)
		}
		if first["name"] != defaultModel.ClientModel {
			t.Errorf("default picker model = %v, want configured default %s", first["name"], defaultModel.ClientModel)
		}
	}

	// The proxy section maps the raw slug through untouched (identity).
	cfg, err := LoadProxyConfig()
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Providers["openrouter"]
	if p == nil {
		t.Fatal("openrouter proxy provider missing")
	}
	if got := p.Models["deepseek/deepseek-v4-flash"]; got != "deepseek/deepseek-v4-flash" {
		t.Errorf("deepseek slug mapping = %q, want identity", got)
	}
}
