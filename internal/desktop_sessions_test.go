package internal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDesktop3pBackupRoundTripKeepsSessionFilesOpaque(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	active := ClaudeDesktop3pDir()
	session := filepath.Join(active, "claude-code-sessions", "account", "org", "local_session.json")
	if err := os.MkdirAll(filepath.Dir(session), 0700); err != nil {
		t.Fatal(err)
	}
	want := []byte(`{"sessionId":"opaque","unknownFutureField":{"v":1}}`)
	if err := os.WriteFile(session, want, 0600); err != nil {
		t.Fatal(err)
	}
	if err := removeDesktop3pDir(); err != nil {
		t.Fatalf("removeDesktop3pDir: %v", err)
	}
	if _, err := os.Stat(active); !os.IsNotExist(err) {
		t.Fatalf("active third-party directory should be inactive: %v", err)
	}
	if err := activateDesktop3pBackup(); err != nil {
		t.Fatalf("activateDesktop3pBackup: %v", err)
	}
	got, err := os.ReadFile(session)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("session file changed across backup round trip: got %q want %q", got, want)
	}
}

func TestDesktopProviderApplyDoesNotCreateSessionLinks(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	nativeSession := filepath.Join(ClaudeDesktopCodeSessionsDir(), "native", "org", "local_native.json")
	if err := os.MkdirAll(filepath.Dir(nativeSession), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nativeSession, []byte(`{"sessionId":"native"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := applyDesktopProvider("deepseek", map[string]interface{}{
		"deployment_mode": "3p",
		"model":           DeepSeekV4FlashModel,
	}); err != nil {
		t.Fatal(err)
	}
	err := filepath.WalkDir(ClaudeDesktop3pCodeSessionsDir(), func(path string, d os.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			t.Fatalf("AIX created a client session symlink: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestProjectMissingDesktopSessionEntriesIsNonOverwriting(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source", "account-3p", "org-3p")
	target := filepath.Join(dir, "target", "account-native", "org-native")
	for _, path := range []string{source, target} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	read := func(path string) string {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	write(filepath.Join(source, "local_shared.json"), `{"source":"3p"}`)
	write(filepath.Join(source, "local_3p_only.json"), `{"source":"3p-only"}`)
	write(filepath.Join(source, "scheduled-tasks.json"), `{"ignored":true}`)
	write(filepath.Join(target, "local_shared.json"), `{"source":"native"}`)

	copied, err := projectMissingDesktopSessionEntries(filepath.Join(dir, "source"), filepath.Join(dir, "target"))
	if err != nil {
		t.Fatal(err)
	}
	if copied != 1 {
		t.Fatalf("copied = %d, want 1", copied)
	}
	if got := read(filepath.Join(target, "local_shared.json")); got != `{"source":"native"}` {
		t.Fatalf("existing native entry overwritten: %s", got)
	}
	if got := read(filepath.Join(target, "local_3p_only.json")); got != `{"source":"3p-only"}` {
		t.Fatalf("projected entry = %s", got)
	}
	if _, err := os.Stat(filepath.Join(target, "scheduled-tasks.json")); !os.IsNotExist(err) {
		t.Fatalf("non-session metadata was projected: %v", err)
	}
	if got := read(filepath.Join(source, "local_3p_only.json")); got != `{"source":"3p-only"}` {
		t.Fatalf("source entry changed: %s", got)
	}
}

func TestProjectDesktopSessionsUsesExistingIdentityWithoutNativeSessions(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source", "account-3p", "org-3p")
	target := filepath.Join(dir, "target", "account-native", "org-native")
	for _, path := range []string{source, target} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, "local_first.json"), []byte(`{"sessionId":"first"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "scheduled-tasks.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}

	copied, err := projectMissingDesktopSessionEntries(filepath.Join(dir, "source"), filepath.Join(dir, "target"))
	if err != nil {
		t.Fatal(err)
	}
	if copied != 1 {
		t.Fatalf("copied = %d, want 1", copied)
	}
	if _, err := os.Stat(filepath.Join(target, "local_first.json")); err != nil {
		t.Fatalf("session not projected into existing native identity: %v", err)
	}
}

func TestRemoveDesktop3pDirProjectsMissingSessionVisibility(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	source := filepath.Join(ClaudeDesktop3pCodeSessionsDir(), "account-3p", "org-3p")
	target := filepath.Join(ClaudeDesktopCodeSessionsDir(), "account-native", "org-native")
	for _, path := range []string{source, target} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, "local_visible.json"), []byte(`{"sessionId":"visible"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "scheduled-tasks.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}

	if err := removeDesktop3pDir(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ClaudeDesktop3pDir()); !os.IsNotExist(err) {
		t.Fatalf("active 3p store still exists: %v", err)
	}
	for _, path := range []string{
		filepath.Join(ClaudeDesktop3pDir()+".bak", "claude-code-sessions", "account-3p", "org-3p", "local_visible.json"),
		filepath.Join(target, "local_visible.json"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected preserved/projected session %s: %v", path, err)
		}
	}
}
