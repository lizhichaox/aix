package cmd

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lizhichaox/aix/internal"
)

type configFileSnapshot struct {
	path   string
	exists bool
	mode   fs.FileMode
	data   []byte
}

type configDirSnapshot struct {
	path   string
	exists bool
	dirs   map[string]fs.FileMode
	files  map[string]configFileSnapshot
}

// claudeConfigTransaction captures only configuration and AIX-owned state.
// Claude session/transcript files remain opaque and are never copied or
// rewritten by the transaction.
type claudeConfigTransaction struct {
	files                []configFileSnapshot
	dirs                 []configDirSnapshot
	active3pExisted      bool
	backup3pExisted      bool
	active3pPath         string
	backup3pPath         string
	backupArchivesBefore map[string]bool
	provider             string
}

func beginClaudeConfigTransaction(provider string) (*claudeConfigTransaction, error) {
	active := internal.ClaudeDesktop3pDir()
	backup := active + ".bak"
	tx := &claudeConfigTransaction{
		active3pExisted:      pathExists(active),
		backup3pExisted:      pathExists(backup),
		active3pPath:         active,
		backup3pPath:         backup,
		backupArchivesBefore: matchingPaths(backup + ".*"),
		provider:             provider,
	}
	filePaths := []string{
		internal.ClaudeSettingsPath(),
		internal.ClaudeCodeNativeSnapshotPath(),
		internal.ClaudeDesktopConfigPath(),
		internal.ClaudeDesktop3pConfigPath(),
		filepath.Join(backup, "claude_desktop_config.json"),
		internal.NativeDesktopSnapPath(),
		internal.ProxyConfigPath(),
		internal.StatePath(),
	}
	if provider != "" {
		filePaths = append(filePaths,
			internal.ProviderPath("claudecode", provider),
			internal.ProviderPath("desktop", provider),
		)
	}
	for _, path := range uniqueNonEmpty(filePaths) {
		snapshot, err := captureConfigFile(path)
		if err != nil {
			return nil, err
		}
		tx.files = append(tx.files, snapshot)
	}
	dirPaths := []string{
		internal.ClaudeDesktop3pConfigLibraryDir(),
		filepath.Join(backup, "configLibrary"),
	}
	for _, path := range dirPaths {
		snapshot, err := captureConfigDir(path)
		if err != nil {
			return nil, err
		}
		tx.dirs = append(tx.dirs, snapshot)
	}
	return tx, nil
}

func (tx *claudeConfigTransaction) Rollback() error {
	var errs []string
	// Applying a provider can atomically rename Claude-3p.bak into the active
	// slot. Restore that location before restoring path-based config snapshots.
	if !tx.active3pExisted {
		switch {
		case tx.backup3pExisted && !pathExists(tx.backup3pPath) && pathExists(tx.active3pPath):
			if err := os.Rename(tx.active3pPath, tx.backup3pPath); err != nil {
				errs = append(errs, fmt.Sprintf("restore Claude-3p backup location: %v", err))
			}
		case !tx.backup3pExisted && pathExists(tx.active3pPath):
			if err := os.RemoveAll(tx.active3pPath); err != nil {
				errs = append(errs, fmt.Sprintf("remove transaction-created Claude-3p data: %v", err))
			}
		}
	} else if !pathExists(tx.active3pPath) && pathExists(tx.backup3pPath) {
		// Native restore parks the active 3p directory in the backup slot.
		// Put it back when a later step in the Claude-wide transaction fails.
		if err := os.Rename(tx.backup3pPath, tx.active3pPath); err != nil {
			errs = append(errs, fmt.Sprintf("reactivate Claude-3p data: %v", err))
		} else if tx.backup3pExisted {
			archive := newestNewPath(tx.backup3pPath+".*", tx.backupArchivesBefore)
			if archive == "" {
				errs = append(errs, "restore previous Claude-3p backup: archived backup not found")
			} else if err := os.Rename(archive, tx.backup3pPath); err != nil {
				errs = append(errs, fmt.Sprintf("restore previous Claude-3p backup: %v", err))
			}
		}
	}
	for _, snapshot := range tx.dirs {
		if err := restoreConfigDir(snapshot); err != nil {
			errs = append(errs, err.Error())
		}
	}
	for _, snapshot := range tx.files {
		if err := restoreConfigFile(snapshot); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("Claude transaction rollback failed: %s", strings.Join(errs, "; "))
	}
	return nil
}

func captureConfigFile(path string) (configFileSnapshot, error) {
	snapshot := configFileSnapshot{path: path}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return snapshot, nil
	}
	if err != nil {
		return snapshot, fmt.Errorf("snapshot %s: %w", path, err)
	}
	if info.IsDir() {
		return snapshot, fmt.Errorf("snapshot %s: expected file", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return snapshot, fmt.Errorf("snapshot %s: %w", path, err)
	}
	snapshot.exists = true
	snapshot.mode = info.Mode().Perm()
	snapshot.data = data
	return snapshot, nil
}

func restoreConfigFile(snapshot configFileSnapshot) error {
	if !snapshot.exists {
		if err := os.Remove(snapshot.path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove new config %s: %w", snapshot.path, err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(snapshot.path), 0700); err != nil {
		return fmt.Errorf("restore config directory %s: %w", snapshot.path, err)
	}
	if err := os.WriteFile(snapshot.path, snapshot.data, snapshot.mode); err != nil {
		return fmt.Errorf("restore config %s: %w", snapshot.path, err)
	}
	return nil
}

func captureConfigDir(root string) (configDirSnapshot, error) {
	snapshot := configDirSnapshot{
		path:  root,
		dirs:  make(map[string]fs.FileMode),
		files: make(map[string]configFileSnapshot),
	}
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return snapshot, nil
	} else if err != nil {
		return snapshot, fmt.Errorf("snapshot directory %s: %w", root, err)
	}
	snapshot.exists = true
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			snapshot.dirs[rel] = info.Mode().Perm()
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("refuse to snapshot config symlink %s", path)
		}
		file, err := captureConfigFile(path)
		if err != nil {
			return err
		}
		file.path = rel
		snapshot.files[rel] = file
		return nil
	})
	if err != nil {
		return snapshot, fmt.Errorf("snapshot directory %s: %w", root, err)
	}
	return snapshot, nil
}

func restoreConfigDir(snapshot configDirSnapshot) error {
	if err := os.RemoveAll(snapshot.path); err != nil {
		return fmt.Errorf("clear config directory %s: %w", snapshot.path, err)
	}
	if !snapshot.exists {
		return nil
	}
	dirs := make([]string, 0, len(snapshot.dirs))
	for rel := range snapshot.dirs {
		dirs = append(dirs, rel)
	}
	sort.Slice(dirs, func(i, j int) bool {
		return strings.Count(dirs[i], string(os.PathSeparator)) < strings.Count(dirs[j], string(os.PathSeparator))
	})
	for _, rel := range dirs {
		path := snapshot.path
		if rel != "." {
			path = filepath.Join(snapshot.path, rel)
		}
		if err := os.MkdirAll(path, snapshot.dirs[rel]); err != nil {
			return fmt.Errorf("restore config directory %s: %w", path, err)
		}
	}
	for rel, file := range snapshot.files {
		file.path = filepath.Join(snapshot.path, rel)
		if err := restoreConfigFile(file); err != nil {
			return err
		}
	}
	return nil
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func matchingPaths(pattern string) map[string]bool {
	paths, _ := filepath.Glob(pattern)
	out := make(map[string]bool, len(paths))
	for _, path := range paths {
		out[path] = true
	}
	return out
}

func newestNewPath(pattern string, before map[string]bool) string {
	paths, _ := filepath.Glob(pattern)
	sort.Sort(sort.Reverse(sort.StringSlice(paths)))
	for _, path := range paths {
		if !before[path] {
			return path
		}
	}
	return ""
}

func uniqueNonEmpty(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
