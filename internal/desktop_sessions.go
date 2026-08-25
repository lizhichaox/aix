package internal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// activateDesktop3pBackup restores the previous third-party data directory so
// its stable account identity and session index survive provider round trips.
func activateDesktop3pBackup() error {
	active := ClaudeDesktop3pDir()
	if _, err := os.Stat(active); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat Claude-3p data: %w", err)
	}
	bak := active + ".bak"
	if _, err := os.Stat(bak); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("stat Claude-3p backup: %w", err)
	}
	if err := os.Rename(bak, active); err != nil {
		return fmt.Errorf("restore Claude-3p backup: %w", err)
	}
	if err := retargetDesktopSessionLinks(bak, active); err != nil {
		return fmt.Errorf("retarget third-party session links: %w", err)
	}
	return nil
}

// bridgeDesktopCodeSessions keeps the native and third-party Desktop session
// indexes aligned across provider switches.
//
// Same-named local session ids (local_<uuid>.*.json) are reconciled so the
// side with the most recent activity wins: its metadata is backed up first and
// then written over the other side, so a session resumed on either provider
// keeps its transcript id and progress when the user switches back. Transcript
// files (the *.jsonl conversation records) are never touched. Session ids that
// exist on only one side still get a symlink on the other side so both modes
// expose the full list.
func bridgeDesktopCodeSessions(leftRoot, rightRoot string) error {
	if err := reconcileDesktopSessions(leftRoot, rightRoot); err != nil {
		return err
	}
	left, err := newestDesktopSessionLeaf(leftRoot, true)
	if err != nil {
		return err
	}
	right, err := newestDesktopSessionLeaf(rightRoot, false)
	if err != nil {
		return err
	}
	if left == "" || right == "" {
		return nil
	}
	if err := linkMissingDesktopSessions(left, right); err != nil {
		return err
	}
	return linkMissingDesktopSessions(right, left)
}

// reconcileDesktopSessions walks both session indexes and, for every local
// session id present on both sides, makes the newer metadata win. The older
// file is backed up before being replaced; neither side's transcript is
// touched. Sessions that exist on only one side are left for the symlink step.
func reconcileDesktopSessions(leftRoot, rightRoot string) error {
	left := collectDesktopSessionFiles(leftRoot)
	right := collectDesktopSessionFiles(rightRoot)
	for name, leftPath := range left {
		rightPath, ok := right[name]
		if !ok {
			continue
		}
		if err := reconcileSessionFile(leftPath, rightPath, name); err != nil {
			return err
		}
	}
	return nil
}

// collectDesktopSessionFiles returns every local_*.json in root keyed by its
// base name. If the same base name appears under more than one account/org
// leaf, the newest file wins. Symlinks are included (they are how missing
// sessions are bridged) so reconcile can also resolve a link whose target is
// the other side.
func collectDesktopSessionFiles(root string) map[string]string {
	files := make(map[string]string)
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasPrefix(name, "local_") || !strings.HasSuffix(name, ".json") {
			return nil
		}
		if prev, ok := files[name]; ok {
			if newerSessionFile(path, prev) {
				files[name] = path
			}
		} else {
			files[name] = path
		}
		return nil
	})
	return files
}

// newerSessionFile reports whether path is more recent than other. A device
// or inode match (one path is a symlink to the other) is never newer.
func newerSessionFile(path, other string) bool {
	pi, err := os.Stat(path)
	if err != nil {
		return false
	}
	oi, err := os.Stat(other)
	if err != nil {
		return false
	}
	if os.SameFile(pi, oi) {
		return false
	}
	return pi.ModTime().After(oi.ModTime())
}

// reconcileSessionFile makes the side with the most recent activity the source
// of truth for a local session id. When the two paths already describe the
// same file (one is a symlink to the other) nothing is done.
func reconcileSessionFile(leftPath, rightPath, name string) error {
	if sameSessionFile(leftPath, rightPath) {
		return nil
	}
	leftFresh := sessionFreshness(leftPath)
	rightFresh := sessionFreshness(rightPath)
	switch {
	case leftFresh > rightFresh:
		return mergeSessionMetadata(leftPath, rightPath, name)
	case rightFresh > leftFresh:
		return mergeSessionMetadata(rightPath, leftPath, name)
	default:
		return nil
	}
}

// sessionContentFields are the metadata fields that describe the conversation
// itself and therefore should follow the side that was last actually worked
// on. Everything else in a local session file is runtime / display state
// (model, effort, permissions, remote tooling, flags) and is deliberately left
// untouched on the destination side so switching providers does not leak a
// third-party model alias, effort level, or bypass-permission mode back into
// the native client.
var sessionContentFields = []string{
	"cliSessionId",
	"sessionId",
	"completedTurns",
	"lastActivityAt",
	"lastFocusedAt",
	"createdAt",
	"title",
	"titleSource",
	"cwd",
	"originCwd",
	"bridgeSessionIds",
	"spawnSeed",
	"permissionModeAcks",
}

// mergeSessionMetadata carries the conversation-content fields from the
// fresher side (source) into the other side (dest) so the user can continue a
// session after switching providers, while preserving the destination's own
// model / effort / permission settings. The destination file is backed up
// before it is rewritten; transcript files are never touched.
func mergeSessionMetadata(source, dest, name string) error {
	srcRaw, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("read %s for %s: %w", source, name, err)
	}
	var src map[string]interface{}
	if err := json.Unmarshal(srcRaw, &src); err != nil {
		return fmt.Errorf("parse %s for %s: %w", source, name, err)
	}
	dstRaw, err := os.ReadFile(dest)
	if err != nil {
		return fmt.Errorf("read %s for %s: %w", dest, name, err)
	}
	var dst map[string]interface{}
	if err := json.Unmarshal(dstRaw, &dst); err != nil {
		return fmt.Errorf("parse %s for %s: %w", dest, name, err)
	}
	merged := make(map[string]interface{}, len(dst))
	for k, v := range dst {
		merged[k] = v
	}
	for _, k := range sessionContentFields {
		if v, ok := src[k]; ok {
			merged[k] = v
		}
	}
	out, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s for %s: %w", dest, name, err)
	}
	out = append(out, '\n')
	return replaceSessionMetadata(dest, out)
}

// sessionFreshness returns a comparable activity time (Unix milliseconds) for a
// local session metadata file so the reconcile step can tell which side carries
// the most recent conversation.
//
// lastActivityAt is authoritative: it advances only when a session is actually
// resumed and continued. lastFocusedAt is a weaker proxy (merely reopening a
// session in the other mode bumps it without advancing the thread) and is used
// only when lastActivityAt is absent. The on-disk modification time is never a
// freshness signal because Claude Desktop touches these metadata files when it
// launches or switches modes, so mtime reflects "when the file was opened", not
// "when the conversation progressed". If the file cannot be read at all the
// mtime is used as a last resort so malformed metadata does not reset a
// session; symlinks are followed so a bridge link resolves to its real file.
func sessionFreshness(path string) int64 {
	raw, err := os.ReadFile(path)
	if err != nil {
		if info, statErr := os.Stat(path); statErr == nil {
			return info.ModTime().UnixMilli()
		}
		return 0
	}
	var meta struct {
		LastActivityAt int64 `json:"lastActivityAt"`
		LastFocusedAt  int64 `json:"lastFocusedAt"`
	}
	if json.Unmarshal(raw, &meta) != nil {
		return 0
	}
	if meta.LastActivityAt > 0 {
		return meta.LastActivityAt
	}
	return meta.LastFocusedAt
}

// replaceSessionMetadata backs up destPath and then writes it as a regular
// file with raw as its contents. Any existing symlink is removed so the write
// never travels through a bridge link into the other side.
func replaceSessionMetadata(destPath string, raw []byte) error {
	if err := backup(destPath, "desktop-session"); err != nil {
		return fmt.Errorf("back up %s: %w", destPath, err)
	}
	if err := os.Remove(destPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale %s: %w", destPath, err)
	}
	if err := os.WriteFile(destPath, raw, 0600); err != nil {
		return fmt.Errorf("write %s: %w", destPath, err)
	}
	return nil
}

// sameSessionFile reports whether a and b resolve to the same underlying file
// (for example one is a symlink to the other).
func sameSessionFile(a, b string) bool {
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(ai, bi)
}

func newestDesktopSessionLeaf(root string, requireLocal bool) (string, error) {
	accounts, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var best string
	var bestTime time.Time
	for _, account := range accounts {
		if !account.IsDir() {
			continue
		}
		accountPath := filepath.Join(root, account.Name())
		orgs, err := os.ReadDir(accountPath)
		if err != nil {
			continue
		}
		for _, org := range orgs {
			if !org.IsDir() {
				continue
			}
			leaf := filepath.Join(accountPath, org.Name())
			entries, err := os.ReadDir(leaf)
			if err != nil {
				continue
			}
			hasLocal := false
			latest := time.Time{}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), "local_") && strings.HasSuffix(entry.Name(), ".json") {
					hasLocal = true
				}
				if info, err := entry.Info(); err == nil && info.ModTime().After(latest) {
					latest = info.ModTime()
				}
			}
			if requireLocal && !hasLocal {
				continue
			}
			if best == "" || latest.After(bestTime) {
				best, bestTime = leaf, latest
			}
		}
	}
	return best, nil
}

func linkMissingDesktopSessions(source, dest string) error {
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dest, 0755); err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "local_") || !strings.HasSuffix(name, ".json") {
			continue
		}
		destPath := filepath.Join(dest, name)
		if _, err := os.Lstat(destPath); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := os.Symlink(filepath.Join(source, name), destPath); err != nil {
			return err
		}
	}
	return nil
}

// retargetDesktopSessionLinks updates only symlinks created by the bridge when
// Claude-3p is renamed to or restored from its backup path.
func retargetDesktopSessionLinks(oldRoot, newRoot string) error {
	root := ClaudeDesktopCodeSessionsDir()
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink == 0 {
			return nil
		}
		target, err := os.Readlink(path)
		if err != nil || !strings.HasPrefix(target, oldRoot+string(os.PathSeparator)) {
			return err
		}
		newTarget := newRoot + strings.TrimPrefix(target, oldRoot)
		if err := os.Remove(path); err != nil {
			return err
		}
		return os.Symlink(newTarget, path)
	})
}
