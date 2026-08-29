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

func runClaudeRestore(cmd *cobra.Command, args []string) error {
	code, err := internal.ResolveHarness("claudecode")
	if err != nil {
		return err
	}
	if err := internal.RestoreNative(code); err != nil {
		return fmt.Errorf("restore %s: %w", code.Name, err)
	}
	if err := internal.SaveAppState("claudecode", ""); err != nil {
		return fmt.Errorf("save Claude Code state: %w", err)
	}
	desktop, err := internal.ResolveHarness("desktop")
	if err != nil {
		return err
	}
	if err := restoreClaudeDesktopNative(desktop); err != nil {
		return err
	}
	fmt.Println("✓ Claude Code + Claude Desktop restored to their native APIs")
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
