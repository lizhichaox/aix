package internal

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyncCodexHistory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	root := `{"timestamp":"2026-08-06T02:00:00Z","type":"session_meta","payload":{"session_id":"root-1","id":"root-1","model_provider":"deepseek","cwd":"/proj/aix"}}` + "\n" +
		`{"timestamp":"2026-08-06T03:00:00Z","type":"event_msg","payload":{"type":"task_complete"}}` + "\n"
	if err := os.MkdirAll(filepath.Join(CodexSessionsDir(), "2026", "08", "06"), 0755); err != nil {
		t.Fatal(err)
	}
	rootPath := filepath.Join(CodexSessionsDir(), "2026", "08", "06", "rollout-root-1.jsonl")
	if err := os.WriteFile(rootPath, []byte(root), 0600); err != nil {
		t.Fatal(err)
	}

	subagent := `{"timestamp":"2026-08-06T02:05:00Z","type":"session_meta","payload":{"session_id":"root-1","id":"sub-9","model_provider":"deepseek","cwd":"/proj/aix"}}` + "\n"
	subPath := filepath.Join(CodexSessionsDir(), "2026", "08", "06", "rollout-sub-9.jsonl")
	if err := os.WriteFile(subPath, []byte(subagent), 0600); err != nil {
		t.Fatal(err)
	}

	archived := `{"timestamp":"2026-08-05T10:00:00Z","type":"session_meta","payload":{"session_id":"arch-2","id":"arch-2","model_provider":"deepseek","cwd":"/proj/proxy"}}` + "\n"
	archPath := filepath.Join(CodexArchivedSessionsDir(), "rollout-arch-2.jsonl")
	if err := os.MkdirAll(filepath.Dir(archPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archPath, []byte(archived), 0600); err != nil {
		t.Fatal(err)
	}

	already := `{"timestamp":"2026-08-06T04:00:00Z","type":"session_meta","payload":{"session_id":"go-3","id":"go-3","model_provider":"opencode-go","cwd":"/proj/go"}}` + "\n"
	alreadyPath := filepath.Join(CodexSessionsDir(), "2026", "08", "06", "rollout-go-3.jsonl")
	if err := os.WriteFile(alreadyPath, []byte(already), 0600); err != nil {
		t.Fatal(err)
	}

	res, err := SyncCodexHistory("opencode-go")
	if err != nil {
		t.Fatalf("SyncCodexHistory: %v", err)
	}
	if res.Retagged != 3 || res.Already != 1 {
		t.Errorf("result = %+v, want 3 retagged (incl. subagent) and 1 already", res)
	}
	for _, p := range []string{rootPath, archPath} {
		meta, ok, err := readSessionMetaFirstLine(p)
		if err != nil || !ok || meta.Payload.ModelProvider != "opencode-go" {
			t.Errorf("provider not retagged in %s: %+v ok=%v err=%v", p, meta, ok, err)
		}
	}
	meta, ok, err := readSessionMetaFirstLine(subPath)
	if err != nil || !ok || meta.Payload.ModelProvider != "opencode-go" {
		t.Errorf("subagent rollout must be retagged with its parent: %+v ok=%v err=%v", meta, ok, err)
	}
	if _, err := os.Stat(res.BackupDir); err != nil {
		t.Errorf("backup dir missing: %v", err)
	}
	if res.DBUpdated {
		t.Error("DB update should be skipped when state_5.sqlite is absent")
	}
	if !strings.Contains(res.DBErr, "not found") {
		t.Errorf("unexpected DB error text: %q", res.DBErr)
	}
}

func TestSyncCodexHistoryPreservesConversationItems(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	const reasoning = `{"timestamp":"2026-08-06T02:00:00Z","type":"session_meta","payload":{"session_id":"root-1","id":"root-1","model_provider":"deepseek","cwd":"/proj/aix"}}` + "\n" +
		`{"timestamp":"2026-08-06T02:01:00Z","ordinal":11,"type":"response_item","payload":{"type":"reasoning","id":"rs-1","summary":[],"content":[{"type":"reasoning_text","text":"hidden thinking"}],"encrypted_content":"abc-0"}}` + "\n" +
		`{"timestamp":"2026-08-06T02:02:00Z","ordinal":12,"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]}}` + "\n"

	if err := os.MkdirAll(filepath.Join(CodexSessionsDir(), "2026", "08", "06"), 0755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(CodexSessionsDir(), "2026", "08", "06", "rollout-root-1.jsonl")
	if err := os.WriteFile(p, []byte(reasoning), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := SyncCodexHistory("openai"); err != nil {
		t.Fatalf("SyncCodexHistory: %v", err)
	}

	content, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	// The provider is retagged.
	meta, ok, err := readSessionMetaFirstLine(p)
	if err != nil || !ok || meta.Payload.ModelProvider != "openai" {
		t.Fatalf("provider not retagged: %+v ok=%v err=%v", meta, ok, err)
	}
	originalItems := strings.SplitN(reasoning, "\n", 2)[1]
	gotItems := strings.SplitN(string(content), "\n", 2)[1]
	if gotItems != originalItems {
		t.Errorf("conversation items changed during provider retag")
	}
}

func TestSyncCodexHistoryLeavesPaginatedLineageByteExact(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := filepath.Join(CodexSessionsDir(), "2026", "08", "25")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	root := `{"timestamp":"2026-08-25T10:00:00Z","type":"session_meta","payload":{"session_id":"thread-1","id":"thread-1","model_provider":"openai"}}` + "\n" +
		`{"timestamp":"2026-08-25T10:01:00Z","type":"event_msg","payload":{"type":"task_complete"}}` + "\n"
	child := `{"timestamp":"2026-08-25T10:02:00Z","type":"session_meta","payload":{"session_id":"thread-1","id":"thread-1","model_provider":"openai","history_base":{"thread_id":"thread-1","end_byte_offset":200,"end_ordinal_exclusive":2}}}` + "\n"
	rootPath := filepath.Join(dir, "rollout-thread-1.jsonl")
	childPath := filepath.Join(dir, "rollout-thread-1-page-2.jsonl")
	if err := os.WriteFile(rootPath, []byte(root), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(childPath, []byte(child), 0600); err != nil {
		t.Fatal(err)
	}

	if _, err := SyncCodexHistory("opencode-go"); err != nil {
		t.Fatalf("SyncCodexHistory: %v", err)
	}
	for path, want := range map[string]string{rootPath: root, childPath: child} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("paginated rollout changed: %s", path)
		}
	}
}

func TestActiveCodexProvider(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if got := ActiveCodexProvider(); got != "" {
		t.Errorf("no config should give empty provider, got %q", got)
	}
	if err := os.MkdirAll(filepath.Dir(CodexConfigPath()), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(CodexConfigPath(), []byte("model_provider = \"opencode-go\"\nmodel = \"gpt-5.6-luna\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := ActiveCodexProvider(); got != "opencode-go" {
		t.Errorf("ActiveCodexProvider = %q, want opencode-go", got)
	}
}

func TestResetThreadHistoryProjection(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	db := threadHistoryProjectionPath()
	if err := os.MkdirAll(filepath.Dir(db), 0755); err != nil {
		t.Fatal(err)
	}
	init := `CREATE TABLE thread_turns (thread_id TEXT, turn_id TEXT, PRIMARY KEY(thread_id, turn_id));
CREATE TABLE thread_items (thread_id TEXT, turn_id TEXT, item_id TEXT, PRIMARY KEY(thread_id, turn_id, item_id));
CREATE TABLE thread_history_projection_state (thread_id TEXT PRIMARY KEY, next_rollout_byte_offset INTEGER, next_rollout_ordinal INTEGER);
INSERT INTO thread_turns VALUES ('t1','a'),('t1','b'),('t2','c');
INSERT INTO thread_items VALUES ('t1','a','i1'),('t2','c','i2');
INSERT INTO thread_history_projection_state VALUES ('t1', 100, 2),('t2', 200, 3);`
	if _, err := exec.Command("sqlite3", db, init).CombinedOutput(); err != nil {
		t.Skipf("sqlite3 unavailable: %v", err)
	}
	if err := resetThreadHistoryProjection([]string{"t1"}); err != nil {
		t.Fatalf("reset: %v", err)
	}
	count := func(table string) int {
		out, _ := exec.Command("sqlite3", db, "SELECT count(*) FROM "+table).CombinedOutput()
		var n int
		fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &n)
		return n
	}
	if c := count("thread_turns"); c != 1 {
		t.Errorf("thread_turns count = %d, want 1 (only t2 remains)", c)
	}
	if c := count("thread_items"); c != 1 {
		t.Errorf("thread_items count = %d, want 1", c)
	}
	if c := count("thread_history_projection_state"); c != 1 {
		t.Errorf("projection_state count = %d, want 1", c)
	}
}

func TestCodexHistorySQLStatements(t *testing.T) {
	stmts := codexHistorySQLStatements("opencode-go", "gpt-5.6-luna")
	if len(stmts) != 2 {
		t.Fatalf("expected 2 statements, got %d: %v", len(stmts), stmts)
	}
	if !strings.Contains(stmts[0], `model_provider = "opencode-go"`) {
		t.Errorf("provider statement wrong: %s", stmts[0])
	}
	if !strings.Contains(stmts[1], `model = "gpt-5.6-luna"`) {
		t.Errorf("model statement wrong: %s", stmts[1])
	}
	// Without a known model, only the provider column is retagged.
	if stmts := codexHistorySQLStatements("opencode-go", ""); len(stmts) != 1 {
		t.Errorf("expected provider-only statements, got %v", stmts)
	}
}
