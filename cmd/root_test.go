package cmd

import (
	"bytes"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func commandNames(cmd *cobra.Command) []string {
	names := make([]string, 0, len(cmd.Commands()))
	for _, child := range cmd.Commands() {
		if child.Name() != "help" {
			names = append(names, child.Name())
		}
	}
	sort.Strings(names)
	return names
}

func TestRootHelpStaysUnderFiftyLines(t *testing.T) {
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	defer rootCmd.SetOut(nil)
	if err := rootCmd.Help(); err != nil {
		t.Fatal(err)
	}
	lines := strings.Count(strings.TrimSuffix(out.String(), "\n"), "\n") + 1
	if lines > 50 {
		t.Fatalf("root help has %d lines, want at most 50:\n%s", lines, out.String())
	}
	for _, removed := range []string{"proxy", "provider", "web", "dsh", "completion", "self-install"} {
		if strings.Contains(out.String(), "  "+removed+" ") {
			t.Errorf("root help still exposes removed command %q", removed)
		}
	}
}

func TestVersionOutputDescribesPurposeAndCommands(t *testing.T) {
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	defer rootCmd.SetOut(nil)
	rootCmd.SetArgs([]string{"--version"})
	defer rootCmd.SetArgs(nil)
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"AIX v",
		"Switch AI providers, models, and reasoning effort across AI harnesses while",
		"keeping your conversations ready to continue across every switch.",
		"aix status",
		"aix restore",
		"aix claude <provider> [model] [effort]",
		"aix codex <provider> [model] [effort]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("version output missing %q:\n%s", want, got)
		}
	}
}

func TestPublicCommandSurface(t *testing.T) {
	if got, want := commandNames(rootCmd), []string{"claude", "codex", "log", "restore", "setup", "status", "usage"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("root commands = %v, want %v", got, want)
	}
	if got, want := commandNames(claudeCmd), []string{"restart", "restore"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("claude commands = %v, want %v", got, want)
	}
	if got, want := commandNames(codexCmd), []string{"restart", "restore"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("codex commands = %v, want %v", got, want)
	}
}

func TestCoreAppsExcludeRemovedIntegrations(t *testing.T) {
	got := make([]string, 0, 3)
	for _, app := range coreApps() {
		got = append(got, app.ID)
	}
	want := []string{"claudecode", "desktop", "codex"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("core apps = %v, want %v", got, want)
	}
}

func TestHarnessFlagsStayAligned(t *testing.T) {
	want := []string{"doctor", "edit", "editor", "effort", "list"}
	for _, command := range []*cobra.Command{claudeCmd, codexCmd} {
		got := make([]string, 0)
		command.Flags().VisitAll(func(flag *pflag.Flag) {
			if flag.Name != "help" {
				got = append(got, flag.Name)
			}
		})
		sort.Strings(got)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s flags = %v, want %v", command.Name(), got, want)
		}
	}
}
