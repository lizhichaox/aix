package cmd

import "testing"

func TestRequireExternalCodexLifecycle(t *testing.T) {
	t.Setenv("CODEX_SESSION_ID", "")
	t.Setenv("CODEX_THREAD_ID", "")
	if err := requireExternalCodexLifecycle(); err != nil {
		t.Fatalf("external terminal rejected: %v", err)
	}

	t.Setenv("CODEX_THREAD_ID", "thread-test")
	if err := requireExternalCodexLifecycle(); err == nil {
		t.Fatal("active Codex task was allowed to restart its host")
	}
}
