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
// is backed up first. Paginated lineages are left byte-for-byte intact because
// their continuations store offsets into earlier rollouts.
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
	paginated := paginatedCodexThreadIDs()
	handle := func(path string) error {
		meta, ok, err := readSessionMetaFirstLine(path)
		if err != nil {
			res.Failed++
			return nil
		}
		if !ok {
			return nil
		}
		threadID := meta.Payload.SessionID
		if threadID == "" {
			threadID = meta.Payload.ID
		}
		// A paginated continuation stores an exact byte offset into its source
		// rollout. Even changing only the source's first-line provider tag moves
		// that boundary, so keep every rollout in such a lineage byte-for-byte
		// intact and rely on the state database's provider tag for visibility.
		if paginated[threadID] {
			res.Already++
			return nil
		}
		// Retag every non-paginated rollout, including subagent rollouts, so the
		// sidebar does not fragment one conversation across provider filters.
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

// paginatedCodexThreadIDs returns every thread participating in a paginated
// lineage. Those rollouts must remain immutable because descendants address
// their source history by byte offset.
func paginatedCodexThreadIDs() map[string]bool {
	ids := make(map[string]bool)
	inspect := func(path string) {
		meta, ok, err := readSessionMetaFirstLine(path)
		if err != nil || !ok || meta.Payload.HistoryBase == nil {
			return
		}
		if id := meta.Payload.HistoryBase.ThreadID; id != "" {
			ids[id] = true
		}
		if id := meta.Payload.SessionID; id != "" {
			ids[id] = true
		} else if id := meta.Payload.ID; id != "" {
			ids[id] = true
		}
	}
	_ = filepath.WalkDir(CodexSessionsDir(), func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(d.Name(), ".jsonl") {
			inspect(path)
		}
		return nil
	})
	if entries, err := os.ReadDir(CodexArchivedSessionsDir()); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".jsonl") {
				inspect(filepath.Join(CodexArchivedSessionsDir(), entry.Name()))
			}
		}
	}
	return ids
}

type sessionMetaLine struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Payload   struct {
		ID            string `json:"id"`
		SessionID     string `json:"session_id"`
		ModelProvider string `json:"model_provider"`
		HistoryBase   *struct {
			ThreadID string `json:"thread_id"`
		} `json:"history_base"`
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

// transformRolloutForProvider returns the rollout with only the session_meta
// provider tag changed. Conversation items are immutable: paginated history
// stores byte offsets into source rollouts, so reserializing reasoning or other
// items can corrupt a thread lineage even when the JSON remains valid.
func transformRolloutForProvider(path, target string) (string, bool, error) {
	original, err := os.ReadFile(path)
	if err != nil {
		return "", false, err
	}
	lines := strings.Split(strings.TrimSuffix(string(original), "\n"), "\n")
	if len(lines) == 0 {
		return string(original), false, nil
	}
	lines[0] = rewriteSessionMeta(lines[0], target)
	newContent := strings.Join(lines, "\n") + "\n"
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
