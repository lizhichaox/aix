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
