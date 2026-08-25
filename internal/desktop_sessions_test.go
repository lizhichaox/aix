package internal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBridgeDesktopCodeSessionsBothDirections(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	nativeLeaf := filepath.Join(ClaudeDesktopCodeSessionsDir(), "native-account", "native-org")
	p3pLeaf := filepath.Join(ClaudeDesktop3pCodeSessionsDir(), "3p-account", "3p-org")
	if err := os.MkdirAll(nativeLeaf, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(p3pLeaf, 0755); err != nil {
		t.Fatal(err)
	}
	nativeSession := filepath.Join(nativeLeaf, "local_native.json")
	p3pSession := filepath.Join(p3pLeaf, "local_3p.json")
	if err := os.WriteFile(nativeSession, []byte(`{"sessionId":"native"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p3pSession, []byte(`{"sessionId":"3p"}`), 0600); err != nil {
		t.Fatal(err)
	}

	if err := bridgeDesktopCodeSessions(ClaudeDesktopCodeSessionsDir(), ClaudeDesktop3pCodeSessionsDir()); err != nil {
		t.Fatal(err)
	}
	assertSessionSymlink(t, filepath.Join(p3pLeaf, "local_native.json"), nativeSession)
	assertSessionSymlink(t, filepath.Join(nativeLeaf, "local_3p.json"), p3pSession)
}

func TestDesktopSessionLinksFollow3pBackupRename(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	nativeLeaf := filepath.Join(ClaudeDesktopCodeSessionsDir(), "native-account", "native-org")
	p3pLeaf := filepath.Join(ClaudeDesktop3pCodeSessionsDir(), "3p-account", "3p-org")
	if err := os.MkdirAll(nativeLeaf, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(p3pLeaf, 0755); err != nil {
		t.Fatal(err)
	}
	p3pSession := filepath.Join(p3pLeaf, "local_3p.json")
	if err := os.WriteFile(p3pSession, []byte(`{"sessionId":"3p"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(p3pSession, filepath.Join(nativeLeaf, "local_3p.json")); err != nil {
		t.Fatal(err)
	}

	bak := ClaudeDesktop3pDir() + ".bak"
	if err := os.Rename(ClaudeDesktop3pDir(), bak); err != nil {
		t.Fatal(err)
	}
	if err := retargetDesktopSessionLinks(ClaudeDesktop3pDir(), bak); err != nil {
		t.Fatal(err)
	}
	assertSessionSymlink(t, filepath.Join(nativeLeaf, "local_3p.json"), filepath.Join(bak, "claude-code-sessions", "3p-account", "3p-org", "local_3p.json"))

	if err := activateDesktop3pBackup(); err != nil {
		t.Fatal(err)
	}
	assertSessionSymlink(t, filepath.Join(nativeLeaf, "local_3p.json"), p3pSession)
}

func TestBridgeReconcilesSameSessionNewestWins(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	nativeLeaf := filepath.Join(ClaudeDesktopCodeSessionsDir(), "native-account", "native-org")
	p3pLeaf := filepath.Join(ClaudeDesktop3pCodeSessionsDir(), "3p-account", "3p-org")
	if err := os.MkdirAll(nativeLeaf, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(p3pLeaf, 0755); err != nil {
		t.Fatal(err)
	}

	// The same session id exists on both sides, but the third-party side
	// carries the most recent activity (e.g. it was continued on DeepSeek).
	old := `{"sessionId":"local_same.json","cliSessionId":"native-cli","lastActivityAt":1000,"completedTurns":7,"model":"claude-opus-5","permissionMode":"auto"}`
	new := `{"sessionId":"local_same.json","cliSessionId":"p3p-cli","lastActivityAt":2000,"completedTurns":30,"model":"claude-fable-aix[1m]","permissionMode":"bypassPermissions"}`
	nativeSession := filepath.Join(nativeLeaf, "local_same.json")
	p3pSession := filepath.Join(p3pLeaf, "local_same.json")
	if err := os.WriteFile(nativeSession, []byte(old), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p3pSession, []byte(new), 0600); err != nil {
		t.Fatal(err)
	}

	if err := bridgeDesktopCodeSessions(ClaudeDesktopCodeSessionsDir(), ClaudeDesktop3pCodeSessionsDir()); err != nil {
		t.Fatalf("bridgeDesktopCodeSessions: %v", err)
	}

	// The native side takes the newest conversation content (cliSessionId,
	// turn count, activity) but keeps its own runtime state (model, permission
	// mode).
	nativeMeta := readSessionMetadata(t, nativeSession)
	if nativeMeta["cliSessionId"] != "p3p-cli" {
		t.Errorf("native cliSessionId = %v, want p3p-cli", nativeMeta["cliSessionId"])
	}
	if nativeMeta["completedTurns"] != float64(30) {
		t.Errorf("native completedTurns = %v, want 30", nativeMeta["completedTurns"])
	}
	if nativeMeta["lastActivityAt"] != float64(2000) {
		t.Errorf("native lastActivityAt = %v, want 2000", nativeMeta["lastActivityAt"])
	}
	if nativeMeta["model"] != "claude-opus-5" {
		t.Errorf("native model = %v, want claude-opus-5 (preserved)", nativeMeta["model"])
	}
	if nativeMeta["permissionMode"] != "auto" {
		t.Errorf("native permissionMode = %v, want auto (preserved)", nativeMeta["permissionMode"])
	}

	// The third-party side keeps its own state throughout.
	p3pMeta := readSessionMetadata(t, p3pSession)
	if p3pMeta["cliSessionId"] != "p3p-cli" || p3pMeta["model"] != "claude-fable-aix[1m]" || p3pMeta["permissionMode"] != "bypassPermissions" {
		t.Errorf("3p metadata = %v, want p3p-cli with deepseek state", p3pMeta)
	}

	// The stale destination must have been backed up before overwrite.
	matches, err := filepath.Glob(filepath.Join(BackupsDir(), "local_same.json.desktop-session.*.bak"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("backup count = %d, want 1", len(matches))
	}
	backupMeta := readSessionMetadata(t, matches[0])
	if backupMeta["cliSessionId"] != "native-cli" || backupMeta["completedTurns"] != float64(7) {
		t.Errorf("backed up metadata = %v, want old native copy", backupMeta)
	}
}

func TestBridgeKeepsExistingSameNameWhenEquallyFresh(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	nativeLeaf := filepath.Join(ClaudeDesktopCodeSessionsDir(), "native-account", "native-org")
	p3pLeaf := filepath.Join(ClaudeDesktop3pCodeSessionsDir(), "3p-account", "3p-org")
	if err := os.MkdirAll(nativeLeaf, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(p3pLeaf, 0755); err != nil {
		t.Fatal(err)
	}
	content := `{"sessionId":"local_same.json","cliSessionId":"native-cli","lastActivityAt":1000}`
	nativeSession := filepath.Join(nativeLeaf, "local_same.json")
	p3pSession := filepath.Join(p3pLeaf, "local_same.json")
	if err := os.WriteFile(nativeSession, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	// One side is a symlink to the other (same underlying file), so the
	// reconcile must not replace it with a regular file.
	if err := os.Symlink(nativeSession, p3pSession); err != nil {
		t.Fatal(err)
	}

	if err := bridgeDesktopCodeSessions(ClaudeDesktopCodeSessionsDir(), ClaudeDesktop3pCodeSessionsDir()); err != nil {
		t.Fatalf("bridgeDesktopCodeSessions: %v", err)
	}
	if info, err := os.Lstat(p3pSession); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("3p session should remain a symlink, mode=%v err=%v", info, err)
	}
}

func TestBridgeReconcilePrefersLastActivityNotTouchTime(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	nativeLeaf := filepath.Join(ClaudeDesktopCodeSessionsDir(), "native-account", "native-org")
	p3pLeaf := filepath.Join(ClaudeDesktop3pCodeSessionsDir(), "3p-account", "3p-org")
	if err := os.MkdirAll(nativeLeaf, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(p3pLeaf, 0755); err != nil {
		t.Fatal(err)
	}

	// Native copy was merely reopened last (its mtime is the newest), but the
	// third-party copy actually carries the continued conversation (newer
	// lastActivityAt). mtime must not win.
	nativeSession := filepath.Join(nativeLeaf, "local_same.json")
	p3pSession := filepath.Join(p3pLeaf, "local_same.json")
	native := `{"sessionId":"local_same.json","cliSessionId":"native-cli","lastActivityAt":1000,"lastFocusedAt":9000,"model":"claude-opus-5","permissionMode":"auto"}`
	p3p := `{"sessionId":"local_same.json","cliSessionId":"p3p-cli","lastActivityAt":2000,"lastFocusedAt":1500,"model":"claude-fable-aix[1m]","permissionMode":"bypassPermissions"}`
	if err := os.WriteFile(nativeSession, []byte(native), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p3pSession, []byte(p3p), 0600); err != nil {
		t.Fatal(err)
	}
	// Bump native's mtime far past the third-party file so the old buggy
	// implementation would wrongly treat native as fresher.
	future := time.Now().Add(24 * time.Hour)
	if err := os.Chtimes(nativeSession, future, future); err != nil {
		t.Fatal(err)
	}

	if err := bridgeDesktopCodeSessions(ClaudeDesktopCodeSessionsDir(), ClaudeDesktop3pCodeSessionsDir()); err != nil {
		t.Fatalf("bridgeDesktopCodeSessions: %v", err)
	}
	// The third-party (continued) content wins on the native side while its
	// runtime state stays native.
	nativeMeta := readSessionMetadata(t, nativeSession)
	if nativeMeta["cliSessionId"] != "p3p-cli" {
		t.Errorf("native cliSessionId = %v, want p3p-cli", nativeMeta["cliSessionId"])
	}
	if nativeMeta["lastActivityAt"] != float64(2000) {
		t.Errorf("native lastActivityAt = %v, want 2000", nativeMeta["lastActivityAt"])
	}
	if nativeMeta["model"] != "claude-opus-5" {
		t.Errorf("native model = %v, want claude-opus-5 (preserved)", nativeMeta["model"])
	}
}

func readSessionMetadata(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func assertSessionSymlink(t *testing.T, path, wantTarget string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s is not a symlink", path)
	}
	target, err := os.Readlink(path)
	if err != nil {
		t.Fatal(err)
	}
	if target != wantTarget {
		t.Fatalf("symlink target = %q, want %q", target, wantTarget)
	}
}
