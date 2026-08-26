package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/lizhichaox/aix/internal"
	"github.com/spf13/cobra"
)

type statusData struct {
	LastSwitch string          `json:"last_switch,omitempty"`
	Issue      string          `json:"issue,omitempty"`
	Harnesses  []harnessStatus `json:"harnesses"`
}

type harnessStatus struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Provider string   `json:"provider"`
	Mode     string   `json:"mode"`
	Model    string   `json:"model,omitempty"`
	Context  int      `json:"context_length,omitempty"`
	Effort   string   `json:"effort,omitempty"`
	Choices  []string `json:"choices,omitempty"`
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show harness status",
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOut, _ := cmd.Flags().GetBool("json")
		state, _ := internal.LoadState()

		var data statusData

		if state.UpdatedAt != "" {
			data.LastSwitch = state.UpdatedAt
		}

		data.Harnesses = buildHarnessStatuses(state)
		if anyManaged(data.Harnesses) {
			if !internal.IsGatewayReady() {
				data.Issue = "AIX gateway is not running; switch the managed provider again to recover"
			}
		}

		if jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(data)
		}

		// Text output
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "  Harness\tType\tProvider\tModel\tContext-Length\tEffort\tOptions")
		for _, a := range data.Harnesses {
			provider := a.Provider
			if provider == "" || provider == "-" {
				provider = "—"
			}
			effort := a.Effort
			if effort == "" {
				effort = "—"
			}
			options := "—"
			if len(a.Choices) > 0 {
				options = strings.Join(a.Choices, ", ")
			}
			model := a.Model
			if model == "" {
				model = "—"
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				a.Name, connectionLabel(a.Mode), provider, model, formatContextLength(a.Context), effort, options)
		}
		if err := tw.Flush(); err != nil {
			return err
		}

		if data.LastSwitch != "" {
			if t, err := time.Parse(time.RFC3339, data.LastSwitch); err == nil {
				fmt.Printf("\nLast switch: %s\n", t.Format("Jan 2 15:04"))
			} else {
				fmt.Printf("\nLast switch: %s\n", data.LastSwitch)
			}
		}
		if data.Issue != "" {
			fmt.Printf("\nWarning: %s.\n", data.Issue)
		}
		return nil
	},
}

func anyManaged(harnesses []harnessStatus) bool {
	for _, harness := range harnesses {
		if harness.Provider != "" && harness.Provider != "-" && connectionLabel(harness.Mode) == "managed" {
			return true
		}
	}
	return false
}

func buildHarnessStatuses(state *internal.State) []harnessStatus {
	return []harnessStatus{
		buildClaudeStatus(state),
		buildCodexStatus(state),
	}
}

func buildClaudeStatus(state *internal.State) harnessStatus {
	status := harnessStatus{ID: internal.HarnessClaude, Name: "Claude", Provider: state.Apps["claudecode"]}
	if status.Provider == "" || status.Provider == "-" {
		status.Provider = state.Apps["desktop"]
	}
	if status.Provider == "" || status.Provider == "-" {
		status.Provider = "anthropic"
		status.Mode = "native"
	} else {
		status.Mode = "gateway"
	}
	status.Effort = statusEffort(internal.HarnessClaude, status.Provider)
	status.Model, status.Context = internal.CurrentHarnessModel(internal.HarnessClaude, status.Provider)
	status.Choices = providerChoices("claudecode", status.Provider)
	return status
}

func buildCodexStatus(state *internal.State) harnessStatus {
	status := harnessStatus{ID: internal.HarnessCodex, Name: "Codex", Provider: state.Apps["codex"]}
	if status.Provider == "" {
		status.Provider = "-"
	}
	if app, err := internal.ResolveHarness("codex"); err == nil {
		mode, provider, _ := app.StatusMode()
		status.Mode = mode
		if provider != "" {
			status.Provider = provider
		}
	}
	if status.Provider == "" || status.Provider == "-" {
		status.Provider = "openai"
		status.Mode = "native"
	}
	status.Effort = statusEffort(internal.HarnessCodex, status.Provider)
	status.Model, status.Context = internal.CurrentHarnessModel(internal.HarnessCodex, status.Provider)
	status.Choices = providerChoices("codex", status.Provider)
	return status
}

func formatContextLength(context int) string {
	if context <= 0 {
		return "unknown"
	}
	const mebibyte = 1024 * 1024
	const kibibyte = 1024
	if context%mebibyte == 0 {
		return fmt.Sprintf("%dM", context/mebibyte)
	}
	if context%kibibyte == 0 {
		return fmt.Sprintf("%dK", context/kibibyte)
	}
	return fmt.Sprintf("%d", context)
}

func providerChoices(appID, current string) []string {
	providers, _ := internal.ListProviders(appID)
	choices := make([]string, 0, len(providers))
	for _, provider := range providers {
		if provider != current {
			choices = append(choices, provider)
		}
	}
	return choices
}

func statusEffort(harnessID, provider string) string {
	if effort := internal.CurrentHarnessEffort(harnessID); effort != "" {
		return effort
	}
	if provider == "" || provider == "-" {
		return ""
	}
	selection, err := internal.ResolveHarnessSelection(harnessID, provider, "", "")
	if err != nil {
		return internal.DefaultHarnessEffort
	}
	return selection.Effort
}

// connectionLabel maps the internal StatusMode to a user-facing connection
// type: native (client's own support, no AIX relay), direct (points straight
// at the upstream API), proxy/gateway (through the AIX proxy layer), or unset.
func connectionLabel(mode string) string {
	switch mode {
	case "responses", "native":
		return "native"
	case "direct":
		return "direct"
	case "proxy", "gateway":
		return "managed"
	}
	return "unset"
}

func init() {
	statusCmd.Flags().Bool("json", false, "Output in JSON format")
	rootCmd.AddCommand(statusCmd)
}
