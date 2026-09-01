package internal

import (
	"encoding/json"
	"os"
	"testing"
)

func TestVerifyClaudeProviderAppliedReadsBothClients(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("DEEPSEEK_API_KEY", "sk-test")
	if err := EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	selection, err := ResolveHarnessSelection(HarnessClaude, "deepseek", DeepSeekV4FlashModel, "high")
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureClaudeProxyProvider("deepseek"); err != nil {
		t.Fatal(err)
	}
	if err := ApplyDeepSeekClaudeCodeWithEffort(selection.UpstreamModel, selection.Effort); err != nil {
		t.Fatal(err)
	}
	if err := ApplyDeepSeekClaudeDesktop(selection.UpstreamModel); err != nil {
		t.Fatal(err)
	}
	if err := VerifyClaudeProviderApplied("deepseek", selection.ClientModel, selection.Effort); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyCodexProviderAppliedReadsPrivateRoute(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := ConfigureCodexProxyWithEffort("deepseek", DeepSeekV4FlashModel, "high", "sk-test"); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCodexProviderApplied("deepseek", DeepSeekV4FlashModel, "high"); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyCodexProviderAppliedRejectsNullWebSearchToolType(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := ConfigureCodexProxyWithEffort("deepseek", DeepSeekV4FlashModel, "high", "sk-test"); err != nil {
		t.Fatal(err)
	}
	path := CodexModelsPath()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var catalog map[string]interface{}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		t.Fatal(err)
	}
	models, _ := catalog["models"].([]interface{})
	first, _ := models[0].(map[string]interface{})
	first["web_search_tool_type"] = nil
	raw, err = json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCodexProviderApplied("deepseek", DeepSeekV4FlashModel, "high"); err == nil {
		t.Fatal("accepted model catalog with web_search_tool_type: null")
	}
}

func TestSaveAppStatesCommitsClaudeTargetsTogether(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := SaveAppStates(map[string]string{"claudecode": "deepseek", "desktop": "deepseek"}); err != nil {
		t.Fatal(err)
	}
	state, err := LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Apps["claudecode"] != "deepseek" || state.Apps["desktop"] != "deepseek" {
		t.Fatalf("partial Claude state: %#v", state.Apps)
	}
}
