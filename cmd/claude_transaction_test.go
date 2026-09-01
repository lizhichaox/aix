package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lizhichaox/aix/internal"
)

func TestClaudeConfigTransactionRollsBackProviderSwitch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := internal.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(internal.ClaudeSettingsPath()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(internal.ClaudeSettingsPath(), []byte(`{"env":{"USER":"original"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	backup := internal.ClaudeDesktop3pDir() + ".bak"
	backupConfig := filepath.Join(backup, "claude_desktop_config.json")
	backupEntry := filepath.Join(backup, "configLibrary", "original.json")
	if err := os.MkdirAll(filepath.Dir(backupEntry), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backupConfig, []byte(`{"deploymentMode":"3p","value":"original"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backupEntry, []byte(`{"value":"original"}`), 0600); err != nil {
		t.Fatal(err)
	}

	tx, err := beginClaudeConfigTransaction("deepseek")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(backup, internal.ClaudeDesktop3pDir()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(internal.ClaudeSettingsPath(), []byte(`{"env":{"AIX":"managed"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(internal.ClaudeDesktop3pEntryPath("new"), []byte(`{"value":"new"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(internal.ProviderPath("desktop", "deepseek")), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(internal.ProviderPath("desktop", "deepseek"), []byte("model = \"changed\"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(internal.ClaudeDesktop3pDir()); !os.IsNotExist(err) {
		t.Fatalf("active Claude-3p directory should be returned to backup: %v", err)
	}
	assertFileContent(t, internal.ClaudeSettingsPath(), `{"env":{"USER":"original"}}`)
	assertFileContent(t, backupConfig, `{"deploymentMode":"3p","value":"original"}`)
	assertFileContent(t, backupEntry, `{"value":"original"}`)
	if _, err := os.Stat(filepath.Join(backup, "configLibrary", "new.json")); !os.IsNotExist(err) {
		t.Fatalf("transaction-created config entry remained: %v", err)
	}
	if _, err := os.Stat(internal.ProviderPath("desktop", "deepseek")); !os.IsNotExist(err) {
		t.Fatalf("transaction-created provider template remained: %v", err)
	}
}

func TestCodexConfigTransactionRollsBackAllOwnedFiles(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := internal.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(internal.CodexConfigPath()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(internal.CodexConfigPath(), []byte("model_provider = \"openai\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	tx, err := beginCodexConfigTransaction("deepseek")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(internal.CodexConfigPath(), []byte("model_provider = \"deepseek\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(internal.CodexModelsPath(), []byte(`{"models":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(internal.CodexNativeSnapshotPath(), []byte(`{"version":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, internal.CodexConfigPath(), "model_provider = \"openai\"\n")
	for _, path := range []string{internal.CodexModelsPath(), internal.CodexNativeSnapshotPath()} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("transaction-created file remained at %s: %v", path, err)
		}
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s = %q, want %q", path, got, want)
	}
}
