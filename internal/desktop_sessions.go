package internal

import (
	"fmt"
	"os"
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
