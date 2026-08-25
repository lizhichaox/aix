package cmd

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/lizhichaox/aix/internal"
)

func quitMacApp(appName string) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("%s restart is macOS-only", strings.ToLower(appName))
	}
	quitScript := fmt.Sprintf(`tell application "%s" to quit`, appName)
	out, err := exec.Command("osascript", "-e", quitScript).CombinedOutput()
	if err != nil {
		if !appNotRunning(string(out)) {
			return fmt.Errorf("quit %s: %v\n%s", appName, err, string(out))
		}
	}
	checkScript := fmt.Sprintf(`tell application "System Events" to count of (every process whose name is "%s")`, appName)
	for i := 0; i < 50; i++ {
		time.Sleep(200 * time.Millisecond)
		out, _ := exec.Command("osascript", "-e", checkScript).CombinedOutput()
		if strings.TrimSpace(string(out)) == "0" {
			return nil
		}
	}
	return fmt.Errorf("timed out waiting for %s to quit", appName)
}

func launchMacApp(appName string) error {
	return exec.Command("open", "-a", appName).Run()
}

// restartMacApp quits and relaunches a macOS application by name.
func restartMacApp(appName string) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("%s restart is macOS-only", strings.ToLower(appName))
	}

	fmt.Printf("Quitting %s... ", appName)
	if err := quitMacApp(appName); err != nil {
		return err
	}
	fmt.Println("done")

	fmt.Printf("Launching %s... ", appName)
	if err := launchMacApp(appName); err != nil {
		return fmt.Errorf("launch %s: %v", appName, err)
	}
	fmt.Println("done")
	fmt.Printf("\u2713 %s restarted\n", appName)
	return nil
}

// autoRestartCodex restarts the Codex desktop app after a config change.
// A restart failure is non-fatal: the config is already applied, so we warn
// and leave the manual command for the user.
func autoRestartCodex() {
	if err := restartMacApp(internal.CodexHostAppName()); err != nil {
		fmt.Printf("  ⚠  Auto-restart failed: %v\n", err)
		fmt.Printf("     Restart Codex manually: aix codex restart\n")
	}
}

// restoreCodexNative quits the Codex host before changing its configuration.
// ChatGPT flushes its in-memory Codex config while quitting, so restoring
// first would let that flush overwrite the native OpenAI settings we just
// wrote. History must also be synced before launch so the host never indexes
// third-party model ids under the ChatGPT account.
func restoreCodexNative(app *internal.HarnessInfo) error {
	appName := app.ClientAppName()
	if appName == "" {
		return fmt.Errorf("no desktop client found for %s", app.Name)
	}

	fmt.Printf("Quitting %s... ", appName)
	if err := quitMacApp(appName); err != nil {
		return err
	}
	fmt.Println("done")

	if err := internal.RestoreNative(app); err != nil {
		return fmt.Errorf("restore %s: %w", app.Name, err)
	}
	if err := internal.SaveAppState("codex", ""); err != nil {
		return fmt.Errorf("save state codex: %w", err)
	}
	syncHistoryAfterSwitch(defaultCodexProvider)

	fmt.Printf("Launching %s... ", appName)
	if err := launchMacApp(appName); err != nil {
		return fmt.Errorf("launch %s: %v", appName, err)
	}
	fmt.Println("done")
	return nil
}

func appNotRunning(msg string) bool {
	for _, sub := range []string{"not running", "-609", "-600"} {
		if strings.Contains(msg, sub) {
			return true
		}
	}
	return false
}

// restartClaudeDesktopWithConfig quits Claude Desktop, re-applies the active
// desktop provider's gateway config, then relaunches. The re-apply after quit
// is required: Claude Desktop rewrites claude_desktop_config.json from its
// in-memory state when it quits, stripping AIX's inferenceGateway* fields.
func restartClaudeDesktopWithConfig() error {
	fmt.Printf("Quitting Claude... ")
	if err := quitMacApp("Claude"); err != nil {
		return err
	}
	fmt.Println("done")

	if err := reapplyClaudeDesktopProvider(); err != nil {
		fmt.Printf("  ⚠  Re-applying gateway config failed: %v\n", err)
	}

	fmt.Printf("Launching Claude... ")
	if err := launchMacApp("Claude"); err != nil {
		return fmt.Errorf("launch Claude: %v", err)
	}
	fmt.Println("done")
	fmt.Printf("\u2713 Claude restarted\n")
	return nil
}

// autoRestartClaudeDesktop restarts Claude Desktop after a gateway or
// native-restore switch. A restart failure is non-fatal: the config is
// already applied, so we warn and leave the manual command for the user.
func autoRestartClaudeDesktop() {
	if err := restartClaudeDesktopWithConfig(); err != nil {
		fmt.Printf("  ⚠  Auto-restart failed: %v\n", err)
		fmt.Printf("     Restart Claude manually: aix claude restart\n")
	}
}

// restoreClaudeDesktopNative quits Claude before changing its configuration.
// Claude flushes its in-memory config while quitting, so restoring first would
// let that flush overwrite the native deployment mode we just wrote.
func restoreClaudeDesktopNative(app *internal.HarnessInfo) error {
	fmt.Printf("Quitting Claude... ")
	if err := quitMacApp("Claude"); err != nil {
		return err
	}
	fmt.Println("done")

	if err := internal.RestoreNative(app); err != nil {
		return fmt.Errorf("restore %s: %w", app.Name, err)
	}
	if err := internal.SaveAppState("desktop", ""); err != nil {
		return fmt.Errorf("save state desktop: %w", err)
	}

	fmt.Printf("Launching Claude... ")
	if err := launchMacApp("Claude"); err != nil {
		return fmt.Errorf("launch Claude: %v", err)
	}
	fmt.Println("done")
	return nil
}

// reapplyClaudeDesktopProvider re-writes the current desktop provider template
// after the app has quit, so the gateway fields survive the app's quit-flush.
// No-op when the desktop app is not on an AIX-managed provider.
func reapplyClaudeDesktopProvider() error {
	state, err := internal.LoadState()
	if err != nil {
		return err
	}
	provider := state.Apps["desktop"]
	if provider == "" || provider == "-" {
		return nil
	}
	app, err := internal.ResolveHarness("desktop")
	if err != nil {
		return err
	}
	return internal.ApplyProvider(app, provider)
}
