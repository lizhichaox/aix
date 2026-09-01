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

func TestRequireExternalClaudeLifecycle(t *testing.T) {
	t.Setenv("CLAUDECODE", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "")
	if err := requireExternalClaudeLifecycle(); err != nil {
		t.Fatalf("external terminal rejected: %v", err)
	}

	markers := []string{"CLAUDECODE", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_ENTRYPOINT"}
	for _, marker := range markers {
		t.Run(marker, func(t *testing.T) {
			t.Setenv(marker, "test")
			if err := requireExternalClaudeLifecycle(); err == nil {
				t.Fatalf("active Claude task identified by %s was allowed to restart its host", marker)
			}
		})
	}
}
