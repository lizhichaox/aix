package internal

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

var migrateConfigDirOnce sync.Once

func HomeDir() string {
	h, err := os.UserHomeDir()
	if err != nil || h == "" {
		fmt.Fprintln(os.Stderr, "fatal: cannot determine home directory")
		os.Exit(1)
	}
	return h
}

func AixDir() string {
	home := HomeDir()
	current := filepath.Join(home, ".aix")
	legacy := filepath.Join(home, ".ats")
	migrateConfigDirOnce.Do(func() {
		if _, err := os.Stat(current); !os.IsNotExist(err) {
			return
		}
		if _, err := os.Stat(legacy); err != nil {
			return
		}
		// Keep existing installations working without overwriting a newer
		// directory. Both paths live under the same home directory, so rename
		// preserves the complete configuration atomically.
		_ = os.Rename(legacy, current)
	})
	return current
}

func BackupsDir() string {
	return filepath.Join(AixDir(), "backups")
}

func StatePath() string {
	return filepath.Join(AixDir(), "state.toml")
}

func ClaudeSettingsPath() string {
	return filepath.Join(HomeDir(), ".claude", "settings.json")
}

// ClaudeCodeNativeSnapshotPath stores the native values of the settings fields
// AIX owns while a managed Claude provider is active. The snapshot deliberately
// excludes every unrelated Claude Code setting and environment variable.
func ClaudeCodeNativeSnapshotPath() string {
	return filepath.Join(AixDir(), "claude_code_native.json")
}

// ClaudeConfigJSONPath returns Claude Code/Desktop's primary config file.
// Claude 2.x persists the active per-session model here (in
// clientDataCacheSlots) rather than in settings.json.
func ClaudeConfigJSONPath() string {
	return filepath.Join(HomeDir(), ".claude.json")
}

// HarnessRegistryPath is the user-editable provider/model/effort mapping
// layered over AIX's bundled harness defaults. Each harness owns its own file
// so that editing one harness can never touch another harness's mappings.
func HarnessRegistryPath(harnessID string) string {
	return filepath.Join(HomeDir(), ".aix", "harnesses-"+harnessID+".toml")
}

// LegacyHarnessRegistryPath is the pre-per-harness single registry file. It is
// read once on first load and migrated into per-harness files.
func LegacyHarnessRegistryPath() string {
	return filepath.Join(HomeDir(), ".aix", "harnesses.toml")
}

// ClaudeProjectsDir returns the directory holding per-project Claude Code
// conversation transcripts (~/.claude/projects).
func ClaudeProjectsDir() string {
	return filepath.Join(HomeDir(), ".claude", "projects")
}

func CodexConfigPath() string {
	return filepath.Join(HomeDir(), ".codex", "config.toml")
}

// CodexNativeSnapshotPath stores the AIX-owned native Codex fields while a
// managed provider is active.
func CodexNativeSnapshotPath() string {
	return filepath.Join(AixDir(), "codex_native.json")
}

// CodexModelsPath is the user-managed catalog shared by the Codex CLI, app,
// and IDE extension.
func CodexModelsPath() string {
	return filepath.Join(HomeDir(), ".codex", "models.json")
}

// CodexModelsCachePath is Codex's native, server-refreshed OpenAI model
// catalog. AIX reads it for native status only and never mutates it.
func CodexModelsCachePath() string {
	return filepath.Join(HomeDir(), ".codex", "models_cache.json")
}

func ClaudeDesktopConfigPath() string {
	return filepath.Join(HomeDir(), "Library", "Application Support", "Claude", "claude_desktop_config.json")
}

func ClaudeDesktop3pConfigPath() string {
	return filepath.Join(HomeDir(), "Library", "Application Support", "Claude-3p", "claude_desktop_config.json")
}

// ClaudeDesktop3pDir is the separate data directory Claude Desktop uses in
// third-party mode (config, config library, conversations, and OAuth).
func ClaudeDesktop3pDir() string {
	return filepath.Join(HomeDir(), "Library", "Application Support", "Claude-3p")
}

func ClaudeDesktopCodeSessionsDir() string {
	return filepath.Join(HomeDir(), "Library", "Application Support", "Claude", "claude-code-sessions")
}

func ClaudeDesktop3pCodeSessionsDir() string {
	return filepath.Join(ClaudeDesktop3pDir(), "claude-code-sessions")
}

// ClaudeDesktop3pConfigLibraryDir holds the applied-config entries that
// activate third-party mode in current Claude Desktop builds.
func ClaudeDesktop3pConfigLibraryDir() string {
	return filepath.Join(ClaudeDesktop3pDir(), "configLibrary")
}

// ClaudeDesktop3pMetaPath is the config library index (_meta.json) that
// records which entry is applied.
func ClaudeDesktop3pMetaPath() string {
	return filepath.Join(ClaudeDesktop3pConfigLibraryDir(), "_meta.json")
}

// ClaudeDesktop3pEntryPath returns the config library entry file for id.
func ClaudeDesktop3pEntryPath(id string) string {
	return filepath.Join(ClaudeDesktop3pConfigLibraryDir(), id+".json")
}

func NativeDesktopSnapPath() string {
	return filepath.Join(AixDir(), "desktop_native.json")
}

func ObsidianConfigPath() string {
	return filepath.Join(HomeDir(), "Library", "Application Support", "obsidian", "obsidian.json")
}
