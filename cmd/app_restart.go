package cmd

import (
	"fmt"
	"os"
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
	if appName == internal.CodexHostAppName() {
		if err := requireExternalCodexLifecycle(); err != nil {
			return err
		}
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

// requireExternalCodexLifecycle prevents a Codex task from terminating the
// desktop process that is currently executing it. Provider changes remain a
// normal external-terminal workflow; this guard only rejects self-restarts.
func requireExternalCodexLifecycle() error {
	if os.Getenv("CODEX_SESSION_ID") == "" && os.Getenv("CODEX_THREAD_ID") == "" {
		return nil
	}
	return fmt.Errorf("cannot switch or restart Codex from inside an active Codex task; run the command from an external terminal")
}

// requireExternalClaudeLifecycle prevents Claude Code from terminating the
// Claude Desktop process while a Claude-hosted task is executing this command.
// CLAUDECODE is the primary Claude Code host marker; the additional markers
// cover hosts that expose more specific session metadata.
func requireExternalClaudeLifecycle() error {
	if os.Getenv("CLAUDECODE") == "" &&
		os.Getenv("CLAUDE_CODE_SESSION_ID") == "" &&
		os.Getenv("CLAUDE_CODE_ENTRYPOINT") == "" {
		return nil
	}
	return fmt.Errorf("cannot switch or restart Claude from inside an active Claude task; run the command from an external terminal")
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
func restoreCodexNative(app *internal.HarnessInfo) (retErr error) {
	appName := app.ClientAppName()
	if appName == "" {
		return fmt.Errorf("no desktop client found for %s", app.Name)
	}
	if err := requireExternalCodexLifecycle(); err != nil {
		return err
	}

	fmt.Printf("Quitting %s... ", appName)
	if err := quitMacApp(appName); err != nil {
		return err
	}
	fmt.Println("done")
	tx, err := beginCodexConfigTransaction("")
	if err != nil {
		_ = launchMacApp(appName)
		return err
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		rollbackErr := tx.Rollback()
		launchErr := launchMacApp(appName)
		if rollbackErr != nil || launchErr != nil {
			retErr = fmt.Errorf("%w (rollback: %v; relaunch: %v)", retErr, rollbackErr, launchErr)
		}
	}()

	if err := internal.RestoreNative(app); err != nil {
		return fmt.Errorf("restore %s: %w", app.Name, err)
	}
	if err := internal.VerifyCodexNativeRestored(); err != nil {
		return fmt.Errorf("verify native Codex configuration: %w", err)
	}
	if err := internal.SaveAppState("codex", ""); err != nil {
		return fmt.Errorf("save state codex: %w", err)
	}
	committed = true

	fmt.Printf("Launching %s... ", appName)
	if err := launchMacApp(appName); err != nil {
		fmt.Printf("failed\n  ⚠  Launch failed: %v\n", err)
		fmt.Printf("     Start %s manually; native Codex configuration remains restored.\n", appName)
		return nil
	}
	fmt.Println("done")
	printCodexSessionCompatibilityNotice()
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
	if err := requireExternalClaudeLifecycle(); err != nil {
		return err
	}
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
