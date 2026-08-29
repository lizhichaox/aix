package cmd

import (
	"github.com/lizhichaox/aix/internal"
	"github.com/spf13/cobra"
)

var codexCmd = &cobra.Command{
	Use:   "codex [provider] [model] [effort]",
	Short: "Switch Codex Responses providers",
	Long: `Switch Codex to a Responses API provider through the private AIX gateway.

  aix codex                              list providers
  aix codex <provider>                   use the provider defaults
  aix codex <provider> <model>           select a model
  aix codex <provider> <model> <effort>  select a model and effort`,
	Args:              cobra.MaximumNArgs(3),
	ValidArgsFunction: codexProviderCompletion,
	RunE:              runCodexProvider,
}

var (
	codexListFlag   bool
	codexEffortFlag string
	codexEditFlag   bool
	codexDoctorFlag bool
	codexEditorFlag string
)

var codexRestartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart the Codex desktop app",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if app, err := internal.ResolveHarness("codex"); err == nil {
			if mode, _, _ := app.StatusMode(); mode == "proxy" {
				if err := ensureAIXGateway(); err != nil {
					return err
				}
			}
		}
		return restartMacApp(internal.CodexHostAppName())
	},
}

func init() {
	codexCmd.AddCommand(codexRestartCmd, codexRestoreCmd)
	codexCmd.Flags().BoolVar(&codexListFlag, "list", false, "show the provider's model mapping")
	codexCmd.Flags().StringVar(&codexEffortFlag, "effort", "", "reasoning effort (uses the provider default when omitted)")
	codexCmd.Flags().BoolVar(&codexEditFlag, "edit", false, "edit provider/model/effort mappings")
	codexCmd.Flags().BoolVar(&codexDoctorFlag, "doctor", false, "validate provider/model/effort mappings")
	codexCmd.Flags().StringVar(&codexEditorFlag, "editor", "", "editor to launch for --edit (overrides $VISUAL/$EDITOR)")
	rootCmd.AddCommand(codexCmd)
}
