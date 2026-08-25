package internal

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// CodexSessionsDir and CodexArchivedSessionsDir are retained for automatic
// history synchronization; AIX no longer exposes session browsing commands.
func CodexSessionsDir() string {
	return filepath.Join(HomeDir(), ".codex", "sessions")
}

func CodexArchivedSessionsDir() string {
	return filepath.Join(HomeDir(), ".codex", "archived_sessions")
}

// CodexHistorySyncResult summarizes a SyncCodexHistory run.
type CodexHistorySyncResult struct {
	Target    string
	Retagged  int
	Already   int
	Failed    int
	BackupDir string
	DBUpdated bool
	DBErr     string

	// ModifiedThreads are the thread ids whose rollouts were rewritten by the
	// sync. Their thread-history projection in thread_history_1.sqlite is built
	// from rollout byte offsets, so rewriting the rollout invalidates it and the
	// host must re-project those threads (see resetThreadHistoryProjection).
	ModifiedThreads []string
}

// ActiveCodexProvider returns the model_provider currently configured in
// ~/.codex/config.toml, or "" when Codex is on the default provider.
func ActiveCodexProvider() string {
	config, err := readTomlMap(CodexConfigPath())
	if err != nil {
		return ""
	}
	p, _ := config["model_provider"].(string)
	return p
}

// SyncCodexHistory retags every Codex conversation thread's model_provider
// (rollout session_meta, plus state_5.sqlite when available) to targetProvider.
//
// The Codex desktop app filters the sidebar thread list by the active
// model_provider (openai/codex#31625), which hides conversations recorded
// under other providers even though the data is intact. Retagging makes the
// full history visible again under the current provider. Every modified file
// is backed up first; subagent rollouts are never touched.
func SyncCodexHistory(targetProvider string) (CodexHistorySyncResult, error) {
	targetProvider = strings.TrimSpace(targetProvider)
	if targetProvider == "" {
		targetProvider = ActiveCodexProvider()
	}
	if targetProvider == "" {
		return CodexHistorySyncResult{}, fmt.Errorf("no target provider given and none is active in %s", CodexConfigPath())
	}
	res := CodexHistorySyncResult{Target: targetProvider}
	res.BackupDir = filepath.Join(BackupsDir(), fmt.Sprintf("codex-history-%s-%s", targetProvider, time.Now().Format("20060102-150405.000")))
	if err := os.MkdirAll(res.BackupDir, 0700); err != nil {
		return res, err
	}

	if err := retagRolloutFiles(targetProvider, res.BackupDir, &res); err != nil {
		return res, err
	}
	// The rollout rewrite changes byte offsets, so the host's per-thread
	// thread-history projection is now stale. Drop the projection rows so the
	// host re-derives them from the (intact) rollouts instead of showing the
	// thread truncated at the divergence point.
	var projectionErr string
	if err := resetThreadHistoryProjection(res.ModifiedThreads); err != nil {
		projectionErr = "thread history projection: " + err.Error()
	}
	model := ""
	if ActiveCodexProvider() == targetProvider {
		if cfg, err := readTomlMap(CodexConfigPath()); err == nil {
			model, _ = cfg["model"].(string)
		}
	}
	res.DBUpdated, res.DBErr = retagSQLiteProvider(targetProvider, model, res.BackupDir)
	if projectionErr != "" {
		if res.DBErr != "" {
			res.DBErr += "; " + projectionErr
		} else {
			res.DBErr = projectionErr
		}
	}
	return res, nil
}

func retagRolloutFiles(target, backupDir string, res *CodexHistorySyncResult) error {
	handle := func(path string) error {
		meta, ok, err := readSessionMetaFirstLine(path)
		if err != nil {
			res.Failed++
			return nil
		}
		if !ok {
			return nil
		}
		// Normalize every rollout, not only those changing provider. The host's
		// sidebar surfaces subagent threads under their own provider tag, so a
		// rollout left on a stale provider fragments the history after a switch
		// (root threads move, subagent threads stay behind). A thread may also
		// already sit on the target provider yet still carry foreign reasoning
		// content from living under another provider earlier; repairing it keeps
		// the sidebar history portable and resumable after a switch.
		newContent, changed, err := transformRolloutForProvider(path, target)
		if err != nil {
			res.Failed++
			return nil
		}
		if !changed {
			res.Already++
			return nil
		}
		rel, err := filepath.Rel(filepath.Dir(CodexSessionsDir()), path)
		if err != nil {
			rel = filepath.Base(path)
		}
		if err := backupFile(path, filepath.Join(backupDir, rel)); err != nil {
			res.Failed++
			return nil
		}
		if err := writePrivateFile(path, []byte(newContent)); err != nil {
			res.Failed++
			return nil
		}
		res.Retagged++
		if id := meta.Payload.SessionID; id != "" {
			res.ModifiedThreads = append(res.ModifiedThreads, id)
		} else if meta.Payload.ID != "" {
			res.ModifiedThreads = append(res.ModifiedThreads, meta.Payload.ID)
		}
		return nil
	}

	if err := filepath.WalkDir(CodexSessionsDir(), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".jsonl") {
			return handle(path)
		}
		return nil
	}); err != nil && !os.IsNotExist(err) {
		return err
	}
	if entries, err := os.ReadDir(CodexArchivedSessionsDir()); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
				continue
			}
			if err := handle(filepath.Join(CodexArchivedSessionsDir(), e.Name())); err != nil {
				return err
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

type sessionMetaLine struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Payload   struct {
		ID            string `json:"id"`
		SessionID     string `json:"session_id"`
		ModelProvider string `json:"model_provider"`
	} `json:"payload"`
}

func readSessionMetaFirstLine(path string) (sessionMetaLine, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return sessionMetaLine{}, false, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	if !scanner.Scan() {
		return sessionMetaLine{}, false, scanner.Err()
	}
	var meta sessionMetaLine
	if err := json.Unmarshal(scanner.Bytes(), &meta); err != nil {
		return sessionMetaLine{}, false, nil
	}
	if meta.Type != "session_meta" {
		return sessionMetaLine{}, false, nil
	}
	return meta, true, nil
}

func backupFile(path, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return writePrivateFile(dest, data)
}

// transformRolloutForProvider returns the serialized content of a rollout after
// porting it to the target native provider: the session_meta model_provider is
// set to target and every reasoning item is normalized to the shape any
// Responses API provider accepts on replay. Providers (and models) are dynamic,
// so the normalization keys on the item type rather than any provider id. It
// also reports whether the content changed, so the caller backs up and writes
// only when there is something to do.
func transformRolloutForProvider(path, target string) (string, bool, error) {
	original, err := os.ReadFile(path)
	if err != nil {
		return "", false, err
	}
	lines := strings.Split(strings.TrimSuffix(string(original), "\n"), "\n")
	out := make([]string, 0, len(lines))
	for i, line := range lines {
		if i == 0 {
			line = rewriteSessionMeta(line, target)
		} else {
			line = normalizeReasoningLine(line)
		}
		out = append(out, line)
	}
	newContent := strings.Join(out, "\n") + "\n"
	return newContent, newContent != string(original), nil
}

// rewriteSessionMeta rewrites the session_meta line's model_provider to target,
// returning the original line when the provider already matches so an
// already-ported thread is left byte-for-byte unchanged.
func rewriteSessionMeta(line, target string) string {
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return line
	}
	payload, ok := raw["payload"].(map[string]interface{})
	if !ok {
		return line
	}
	if payload["model_provider"] == target {
		return line
	}
	payload["model_provider"] = target
	raw["payload"] = payload
	out, err := json.Marshal(raw)
	if err != nil {
		return line
	}
	return string(out)
}

// normalizeReasoningLine rewrites a single rollout line, normalizing a
// response_item of type "reasoning" into the shape every Responses API provider
// accepts when replaying history. Native providers differ in how they store
// reasoning: OpenAI keeps only a summary plus an encrypted_content blob, while
// others (DeepSeek, OpenCode, OpenRouter) store the plaintext thinking in the
// content array. The Responses API rejects a non-empty reasoning content array
// on replay (content must have length 0), so a thread retagged to a different
// provider fails with "array_above_max_length". Moving the plaintext thinking
// into summary and dropping provider-specific encrypted_content makes the item
// portable. Lines without that change are returned byte-for-byte unchanged.
func normalizeReasoningLine(line string) string {
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return line
	}
	payload, ok := raw["payload"].(map[string]interface{})
	if raw["type"] != "response_item" || !ok || payload["type"] != "reasoning" {
		return line
	}
	changed := false
	// Preserve the plaintext thinking as a summary when no summary exists yet.
	if content, ok := payload["content"].([]interface{}); ok && len(content) > 0 {
		changed = true
		if existing, _ := payload["summary"].([]interface{}); len(existing) == 0 {
			summary := make([]interface{}, 0, len(content))
			for _, part := range content {
				pm, ok := part.(map[string]interface{})
				if !ok {
					continue
				}
				if text, ok := pm["text"].(string); ok && text != "" {
					summary = append(summary, map[string]interface{}{
						"type": "summary_text",
						"text": text,
					})
				}
			}
			if len(summary) > 0 {
				payload["summary"] = summary
			}
		}
		// Empty the content array so the API's max-0 constraint is satisfied.
		payload["content"] = []interface{}{}
	}
	// The encrypted blob is only decryptable by the provider that created it,
	// so it does not survive a port to another provider.
	if _, ok := payload["encrypted_content"]; ok {
		changed = true
		delete(payload, "encrypted_content")
	}
	if !changed {
		return line
	}
	raw["payload"] = payload
	out, err := json.Marshal(raw)
	if err != nil {
		return line
	}
	return string(out)
}

// threadHistoryProjectionPath is the desktop app's paginated thread-history
// projection database. It is derived from the rollouts and keyed by thread_id;
// its rows carry rollout byte offsets, so rewriting a rollout invalidates it.
func threadHistoryProjectionPath() string {
	return filepath.Join(HomeDir(), ".codex", "thread_history_1.sqlite")
}

// resetThreadHistoryProjection drops the thread-history projection rows for the
// given thread ids so the host re-projects those threads from their rollouts.
// Best-effort: rollouts are the source of truth and a failure only means the
// host keeps serving the stale projection (reported, never fails the sync).
func resetThreadHistoryProjection(threadIDs []string) error {
	if len(threadIDs) == 0 {
		return nil
	}
	dbPath := threadHistoryProjectionPath()
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return nil
	}
	in := make([]string, 0, len(threadIDs))
	for _, id := range threadIDs {
		if id != "" {
			in = append(in, fmt.Sprintf("%q", id))
		}
	}
	if len(in) == 0 {
		return nil
	}
	for _, table := range []string{"thread_turns", "thread_items", "thread_history_projection_state"} {
		stmt := fmt.Sprintf("DELETE FROM %s WHERE thread_id IN (%s);", table, strings.Join(in, ", "))
		if out, err := exec.Command("sqlite3", dbPath, stmt).CombinedOutput(); err != nil {
			return fmt.Errorf("%s: %v (%s)", table, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// retagSQLiteProvider updates the threads.model_provider (and, when the
// active config model is known, threads.model) columns in the desktop app's
// state database via the macOS sqlite3 CLI. It is best-effort: rollouts are
// the durable source of truth and the app rebuilds the DB from them, so a DB
// failure is reported but does not fail the sync.
func retagSQLiteProvider(target, model, backupDir string) (bool, string) {
	dbPath := filepath.Join(HomeDir(), ".codex", "state_5.sqlite")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return false, "state_5.sqlite not found (skip)"
	}
	backupPath := filepath.Join(backupDir, "state_5.sqlite")
	if out, err := exec.Command("sqlite3", dbPath, fmt.Sprintf(".backup %q", backupPath)).CombinedOutput(); err != nil {
		return false, fmt.Sprintf("backup state_5.sqlite: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	for _, stmt := range codexHistorySQLStatements(target, model) {
		if out, err := exec.Command("sqlite3", dbPath, stmt).CombinedOutput(); err != nil {
			return false, fmt.Sprintf("update state_5.sqlite: %v (%s)", err, strings.TrimSpace(string(out)))
		}
	}
	return true, ""
}

// codexHistorySQLStatements builds the SQL that retags the app's thread index
// to the target provider (and, when known, the active model).
func codexHistorySQLStatements(target, model string) []string {
	stmts := []string{
		fmt.Sprintf("UPDATE threads SET model_provider = %q WHERE model_provider IS NOT NULL AND model_provider != %q;", target, target),
	}
	if model != "" {
		stmts = append(stmts, fmt.Sprintf("UPDATE threads SET model = %q WHERE model IS NOT NULL AND model != %q;", model, model))
	}
	return stmts
}
