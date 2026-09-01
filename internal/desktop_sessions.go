package internal

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// activateDesktop3pBackup restores the complete third-party Desktop data
// directory saved by restoreDesktopNative. AIX deliberately treats this
// directory as an opaque unit: it never rewrites, merges, links, or otherwise
// mutates individual client-owned session files inside it.
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
	return nil
}

// projectDesktop3pSessionsToNative keeps third-party Desktop sessions visible
// after native restore. It copies only opaque local_*.json index entries that
// do not already exist in the active native account/org directory. Existing
// native entries and the complete third-party store are never modified.
func projectDesktop3pSessionsToNative() (int, error) {
	source := filepath.Join(ClaudeDesktop3pDir()+".bak", "claude-code-sessions")
	return projectMissingDesktopSessionEntries(source, ClaudeDesktopCodeSessionsDir())
}

func projectMissingDesktopSessionEntries(sourceRoot, targetRoot string) (int, error) {
	targetDir, err := newestDesktopSessionDirectory(targetRoot)
	if err != nil || targetDir == "" {
		return 0, err
	}
	copied := 0
	err = filepath.WalkDir(sourceRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "local_") || !strings.HasSuffix(entry.Name(), ".json") {
			return nil
		}
		dest := filepath.Join(targetDir, entry.Name())
		if _, err := os.Stat(dest); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		created, err := writeExclusiveDesktopSessionEntry(dest, data)
		if err != nil {
			return err
		}
		if created {
			copied++
		}
		return nil
	})
	if os.IsNotExist(err) {
		return 0, nil
	}
	return copied, err
}

func writeExclusiveDesktopSessionEntry(path string, data []byte) (bool, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	written := false
	defer func() {
		_ = f.Close()
		if !written {
			_ = os.Remove(path)
		}
	}()
	if _, err := f.Write(data); err != nil {
		return false, err
	}
	if err := f.Close(); err != nil {
		return false, err
	}
	written = true
	return true, nil
}

// newestDesktopSessionDirectory selects an existing native account/org slot
// without inventing client identity directories. The newest local session is
// the slot Claude Desktop most recently used.
func newestDesktopSessionDirectory(root string) (string, error) {
	var selected string
	var selectedMod int64
	var fallback string
	var fallbackMod int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.Name() == "scheduled-tasks.json" && (fallback == "" || info.ModTime().UnixNano() > fallbackMod) {
			fallback = filepath.Dir(path)
			fallbackMod = info.ModTime().UnixNano()
		}
		if !strings.HasPrefix(entry.Name(), "local_") || !strings.HasSuffix(entry.Name(), ".json") {
			return nil
		}
		if selected == "" || info.ModTime().UnixNano() > selectedMod {
			selected = filepath.Dir(path)
			selectedMod = info.ModTime().UnixNano()
		}
		return nil
	})
	if os.IsNotExist(err) {
		return "", nil
	}
	if err == nil && selected == "" {
		selected = fallback
	}
	return selected, err
}
