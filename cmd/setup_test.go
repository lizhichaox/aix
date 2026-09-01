package cmd

import (
	"os"
	"strings"
	"testing"

	"github.com/lizhichaox/aix/internal"
)

func TestSetupWithoutCredentialsStaysNonDestructive(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	clearNativeProviderKeys(t)
	for _, key := range []string{"DEEPSEEK_API_KEY", "OPENROUTER_API_KEY"} {
		t.Setenv(key, "")
	}

	oldStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	os.Stdin = r
	defer func() {
		os.Stdin = oldStdin
		r.Close()
	}()

	out := captureStdout(t, func() {
		if err := runSetup(setupCmd, nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "Setup complete") {
		t.Fatalf("unexpected setup output:\n%s", out)
	}
	if !strings.Contains(out, "No provider credentials found") {
		t.Fatalf("setup did not show the missing-credentials warning:\n%s", out)
	}
	if !strings.Contains(out, "opencode-zen") || !strings.Contains(out, "not configured") {
		t.Fatalf("setup did not report skipped providers:\n%s", out)
	}
	if strings.Contains(out, "Enter to skip") {
		t.Fatalf("setup must not prompt for input:\n%s", out)
	}
	if _, err := os.Stat(internal.AixDir()); err != nil {
		t.Fatalf("setup did not initialize AIX directory: %v", err)
	}
	if internal.IsServiceInstalled() {
		t.Fatal("setup must not install a gateway service without credentials")
	}
}

func TestSetupHarnessProviderSelectionIsUnambiguous(t *testing.T) {
	configured := []string{"deepseek"}
	if got := setupHarnessProvider(configured, ""); got != "deepseek" {
		t.Errorf("native harness with one credential selected %q", got)
	}
	if got := setupHarnessProvider([]string{"deepseek", "openrouter"}, ""); got != "" {
		t.Errorf("native harness with multiple credentials should stay native, got %q", got)
	}
	if got := setupHarnessProvider([]string{"deepseek", "openrouter"}, "openrouter"); got != "openrouter" {
		t.Errorf("existing configured selection = %q, want openrouter", got)
	}
	if got := setupHarnessProvider(configured, "missing"); got != "" {
		t.Errorf("unavailable existing selection should not be guessed, got %q", got)
	}
}

func TestSetupClaudeProviderRequiresUnifiedState(t *testing.T) {
	configured := []string{"deepseek", "openrouter"}
	state := &internal.State{Apps: map[string]string{"claudecode": "deepseek", "desktop": "deepseek"}}
	if got := setupClaudeProvider(configured, state); got != "deepseek" {
		t.Errorf("unified Claude state = %q, want deepseek", got)
	}
	state.Apps["desktop"] = "openrouter"
	if got := setupClaudeProvider(configured, state); got != "" {
		t.Errorf("split Claude state must not be auto-applied, got %q", got)
	}
}
