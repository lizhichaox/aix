package cmd

import (
	"fmt"

	"github.com/lizhichaox/aix/internal"
	"github.com/spf13/cobra"
)

var claudeCmd = &cobra.Command{
	Use:   "claude [provider] [model] [effort]",
	Short: "Switch Claude Code and Claude Desktop together",
	Long: `Switch Claude Code and Claude Desktop as one harness.

  aix claude                              list providers
  aix claude <provider>                   use the provider defaults
  aix claude <provider> <model>           select a model
  aix claude <provider> <model> <effort>  select a model and effort

Provider switches always apply to both Claude clients.`,
	Args:              cobra.MaximumNArgs(3),
	ValidArgsFunction: claudeProviderCompletion,
	RunE:              claudeRunE,
}

var claudeRestoreCmd = &cobra.Command{
	Use:   "restore",
	Short: "Restore both Claude clients to their native APIs",
	Args:  cobra.NoArgs,
	RunE:  runClaudeRestore,
}

func runClaudeRestore(cmd *cobra.Command, args []string) (retErr error) {
	if err := requireExternalClaudeLifecycle(); err != nil {
		return err
	}
	code, err := internal.ResolveHarness("claudecode")
	if err != nil {
		return err
	}
	desktop, err := internal.ResolveHarness("desktop")
	if err != nil {
		return err
	}
	// Claude Desktop flushes its in-memory configuration on quit. Capture the
	// transaction only after that flush so rollback restores the real pre-command
	// state rather than a stale on-disk copy.
	fmt.Printf("Quitting Claude... ")
	if err := quitMacApp("Claude"); err != nil {
		return err
	}
	fmt.Println("done")
	tx, err := beginClaudeConfigTransaction("")
	if err != nil {
		_ = launchMacApp("Claude")
		return fmt.Errorf("begin Claude restore transaction: %w", err)
	}
	committed := false
	relaunched := false
	defer func() {
		if committed {
			return
		}
		rollbackErr := tx.Rollback()
		var launchErr error
		if !relaunched {
			launchErr = launchMacApp("Claude")
		}
		if rollbackErr != nil || launchErr != nil {
			retErr = fmt.Errorf("%w (rollback: %v; relaunch: %v)", retErr, rollbackErr, launchErr)
		}
	}()
	if err := internal.RestoreNative(code); err != nil {
		return fmt.Errorf("restore %s: %w", code.Name, err)
	}
	if err := internal.RestoreNative(desktop); err != nil {
		return fmt.Errorf("restore %s: %w", desktop.Name, err)
	}
	if err := internal.SaveAppStates(map[string]string{"claudecode": "", "desktop": ""}); err != nil {
		return fmt.Errorf("save Claude harness state: %w", err)
	}
	if err := launchMacApp("Claude"); err != nil {
		return fmt.Errorf("launch Claude: %w", err)
	}
	relaunched = true
	committed = true
	fmt.Println("Launching Claude... done")
	fmt.Println("✓ Claude Code + Claude Desktop restored to their native APIs")
	fmt.Println("  Third-party Desktop sessions remain preserved and missing history entries are shown in native mode.")
	return nil
}

var claudeRestartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart Claude Desktop and re-apply the active provider",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if activeClaudeProvider("desktop") != "" {
			if err := ensureAIXGateway(); err != nil {
				return err
			}
		}
		return restartClaudeDesktopWithConfig()
	},
}

func init() {
	claudeCmd.AddCommand(claudeRestoreCmd, claudeRestartCmd)
	addClaudeFlags(claudeCmd)
	rootCmd.AddCommand(claudeCmd)
}
