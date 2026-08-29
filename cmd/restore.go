package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

var restoreCmd = &cobra.Command{
	Use:   "restore",
	Short: "Restore every harness to its native API",
	Long:  "Restore Claude Code, Claude Desktop, and Codex to their native APIs.",
	Args:  cobra.NoArgs,
	RunE:  runRestoreAll,
}

func runRestoreAll(cmd *cobra.Command, args []string) error {
	var restoreErrors []error
	if err := runClaudeRestore(cmd, nil); err != nil {
		restoreErrors = append(restoreErrors, fmt.Errorf("claude: %w", err))
	}
	if err := runCodexRestore(cmd, nil); err != nil {
		restoreErrors = append(restoreErrors, fmt.Errorf("codex: %w", err))
	}
	if err := errors.Join(restoreErrors...); err != nil {
		return fmt.Errorf("restore all harnesses: %w", err)
	}
	fmt.Println("✓ All harnesses restored to their native APIs")
	return nil
}

func init() {
	rootCmd.AddCommand(restoreCmd)
}
