package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
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
		if claudeManaged(data.Harnesses) {
			if running, _ := internal.IsProxyRunning(); !running {
				data.Issue = "Claude gateway is not running; switch the Claude provider again to recover"
			}
		}

		if jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(data)
		}

		// Text output
		sep := "  "

		fmt.Printf("%s%-22s %-8s %-12s %-8s %s\n", sep, "Harness", "Type", "Provider", "Effort", "Options")
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
			fmt.Printf("%s%-22s %-8s %-12s %-8s %s\n",
				sep, a.Name, connectionLabel(a.Mode), provider, effort, options)
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

func claudeManaged(harnesses []harnessStatus) bool {
	for _, harness := range harnesses {
		if harness.ID == internal.HarnessClaude && harness.Provider != "" && harness.Provider != "-" && harness.Mode != "native" {
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
	status.Effort = statusEffort(internal.HarnessCodex, status.Provider)
	status.Choices = providerChoices("codex", status.Provider)
	return status
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
