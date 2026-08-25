package cmd

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/lizhichaox/aix/internal"
)

func clearNativeProviderKeys(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"DEEPSEEK_API_KEY",
		"OPENCODE_ZEN_API_KEY",
		"OPENCODE_GO_API_KEY",
		"OPENCODE_API_KEY",
		"OPENROUTER_API_KEY",
	} {
		t.Setenv(key, "")
	}
}

func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()
	f()
	w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestCodexProviderCommandListsModelsWithFlag(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	clearNativeProviderKeys(t)
	rootCmd.SetArgs([]string{"codex", "opencode-go", "--list"})
	out := captureStdout(t, func() {
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
	})
	if !strings.Contains(out, "codex / opencode-go mapping") || !strings.Contains(out, "gpt-5.6-luna") || !strings.Contains(out, "Default effort: high") {
		t.Errorf("unexpected output:\n%s", out)
	}
}

func TestCodexProviderOverview(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	clearNativeProviderKeys(t)

	out := codexProviderOverview()
	for _, want := range []string{
		"deepseek",
		"opencode-go",
		"opencode-zen",
		"openrouter",
		"aix codex <provider>",
		"aix codex <provider> <model>",
		"aix codex <provider> --list",
		"aix codex restore",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("overview missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "Responses providers through the private AIX gateway") {
		t.Errorf("overview missing managed Responses section:\n%s", out)
	}
}

func TestCodexProviderCommandSwitchesWithDefaultModel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	clearNativeProviderKeys(t)
	codexListFlag = false
	// One argument must attempt a switch to the provider's default model; with
	// no API key configured the apply fails before any app restart or history
	// sync, proving the command no longer just lists models.
	rootCmd.SetArgs([]string{"codex", "opencode-go"})
	err := rootCmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "API key not found") {
		t.Fatalf("expected one-arg form to attempt a switch and fail on the missing key, got %v", err)
	}
}

func TestCodexBareCommandListsProviders(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	clearNativeProviderKeys(t)
	rootCmd.SetArgs([]string{"codex"})
	out := captureStdout(t, func() {
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
	})
	if !strings.Contains(out, "Responses providers through the private AIX gateway") ||
		!strings.Contains(out, "opencode-zen") ||
		strings.Contains(out, "direct Responses API") {
		t.Errorf("bare codex should list providers:\n%s", out)
	}
}

func TestSyncHistoryAfterSwitch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	clearNativeProviderKeys(t)
	sessionsDir := internal.CodexSessionsDir()
	if err := os.MkdirAll(sessionsDir, 0755); err != nil {
		t.Fatal(err)
	}
	root := `{"timestamp":"2026-08-06T02:00:00Z","type":"session_meta","payload":{"session_id":"auto-sync-1","id":"auto-sync-1","model_provider":"deepseek","cwd":"/proj/aix"}}` + "\n"
	if err := os.WriteFile(sessionsDir+"/rollout-auto-sync-1.jsonl", []byte(root), 0600); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		syncHistoryAfterSwitch("opencode-go")
	})
	if !strings.Contains(out, "conversation history synced to \"opencode-go\"") ||
		!strings.Contains(out, "1 threads") {
		t.Errorf("auto-sync output unexpected:\n%s", out)
	}
	raw, err := os.ReadFile(sessionsDir + "/rollout-auto-sync-1.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"model_provider":"opencode-go"`) {
		t.Errorf("rollout was not retagged:\n%s", raw)
	}
}

func TestCodexProviderCommandUnknownProvider(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	clearNativeProviderKeys(t)
	rootCmd.SetArgs([]string{"codex", "nope"})
	err := rootCmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "unknown Codex Responses provider") {
		t.Fatalf("expected unknown-provider error, got %v", err)
	}
}

func TestCodexProviderCommandRejectsInvalidModel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	clearNativeProviderKeys(t)
	rootCmd.SetArgs([]string{"codex", "deepseek", "bogus-model"})
	err := rootCmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "unsupported DeepSeek Codex model") {
		t.Fatalf("expected model validation error, got %v", err)
	}
}
