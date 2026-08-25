package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/lizhichaox/aix/internal"
	"github.com/spf13/cobra"
)

var logCmd = &cobra.Command{
	Use:   "log",
	Short: "View the Claude gateway log",
	Long:  "View the Claude gateway log. Follows output by default; use --no-follow to print and exit.",
	RunE: func(cmd *cobra.Command, args []string) error {
		logPath := internal.ProxyLogPath()
		lines, _ := cmd.Flags().GetInt("lines")
		noFollow, _ := cmd.Flags().GetBool("no-follow")
		provider, _ := cmd.Flags().GetString("provider")

		if _, err := os.Stat(logPath); os.IsNotExist(err) {
			return fmt.Errorf("Claude gateway log not found at %s\n  Switch a Claude provider to start it", logPath)
		}

		tailArgs := []string{}
		if lines > 0 {
			tailArgs = append(tailArgs, "-n", fmt.Sprintf("%d", lines))
		}
		if !noFollow {
			tailArgs = append(tailArgs, "-f")
		}
		tailArgs = append(tailArgs, logPath)

		tailCmd := exec.Command("tail", tailArgs...)
		tailCmd.Stderr = os.Stderr

		var patterns []string
		if provider != "" {
			patterns = append(patterns, provider)
		}

		if len(patterns) > 0 {
			grepCmd := exec.Command("grep", "--color=never", "-E", strings.Join(patterns, "|"))
			grepCmd.Stdin, _ = tailCmd.StdoutPipe()
			grepCmd.Stdout = os.Stdout
			grepCmd.Stderr = os.Stderr
			tailCmd.Start()
			defer tailCmd.Wait()
			return grepCmd.Run()
		}

		tailCmd.Stdout = os.Stdout
		return tailCmd.Run()
	},
}

func init() {
	logCmd.Flags().IntP("lines", "n", 50, "Number of lines to show")
	logCmd.Flags().Bool("no-follow", false, "Print and exit without following")
	logCmd.Flags().StringP("provider", "p", "", "Filter by provider name")
	rootCmd.AddCommand(logCmd)
}
