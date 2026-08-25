//go:build darwin

package internal

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"text/template"
	"time"
)

const launchdLabel = "com.aix.proxy"

var plistTmpl = template.Must(template.New("plist").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>{{.Label}}</string>
	<key>ProgramArguments</key>
	<array>
		<string>{{.Binary}}</string>
	</array>
	<key>EnvironmentVariables</key>
	<dict>
		<key>AIX_INTERNAL_PROXY</key>
		<string>1</string>
	</dict>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
	<string>{{.LogPath}}</string>
	<key>StandardErrorPath</key>
	<string>{{.LogPath}}</string>
	<key>WorkingDirectory</key>
	<string>/</string>
</dict>
</plist>
`))

func LaunchdPlistPath() string {
	return filepath.Join(HomeDir(), "Library", "LaunchAgents", launchdLabel+".plist")
}

func IsLaunchdInstalled() bool {
	_, err := os.Stat(LaunchdPlistPath())
	return err == nil
}

func IsLaunchdLoaded() bool {
	err := exec.Command("launchctl", "list", launchdLabel).Run()
	return err == nil
}

func InstallLaunchd() (string, error) {
	binary, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("cannot find aix binary: %w", err)
	}
	binary, _ = filepath.Abs(binary)
	binary, _ = filepath.EvalSymlinks(binary)

	// Stop a launchd-managed process by unloading the job first. Sending
	// SIGTERM while KeepAlive is active makes launchd immediately spawn a
	// replacement, which races with the subsequent unload/load and can expose a
	// transient healthy process to readiness checks.
	loaded := IsLaunchdLoaded()
	if loaded {
		out, unloadErr := exec.Command("launchctl", "unload", LaunchdPlistPath()).CombinedOutput()
		if unloadErr != nil {
			return "", fmt.Errorf("launchctl unload: %w\n%s", unloadErr, string(out))
		}
	} else if running, pid := IsProxyRunning(); running {
		// A process with no loaded launchd job is an older standalone gateway.
		// Stop it explicitly before installing the managed service.
		p, _ := os.FindProcess(pid)
		p.Signal(syscall.SIGTERM)
		died := false
		for i := 0; i < 20; i++ {
			if err := p.Signal(syscall.Signal(0)); err != nil {
				died = true
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !died {
			p.Signal(syscall.SIGKILL)
			// Give the kernel a brief moment to reap the process.
			for i := 0; i < 10; i++ {
				if err := p.Signal(syscall.Signal(0)); err != nil {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
		}
	}
	RemovePidFile()

	os.MkdirAll(filepath.Dir(LaunchdPlistPath()), 0755)
	os.MkdirAll(AixDir(), 0755)

	f, err := os.Create(LaunchdPlistPath())
	if err != nil {
		return "", fmt.Errorf("create plist: %w", err)
	}

	data := struct {
		Label, Binary, LogPath string
	}{launchdLabel, binary, ProxyLogPath()}

	if err := plistTmpl.Execute(f, data); err != nil {
		f.Close()
		return "", fmt.Errorf("write plist: %w", err)
	}
	f.Close()

	out, err := exec.Command("launchctl", "load", LaunchdPlistPath()).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("launchctl load: %w\n%s", err, string(out))
	}

	return binary, nil
}

func RestartLaunchd() error {
	plistPath := LaunchdPlistPath()
	if _, err := os.Stat(plistPath); os.IsNotExist(err) {
		return fmt.Errorf("AIX gateway service is not installed; run aix setup")
	}
	exec.Command("launchctl", "unload", plistPath).Run()
	out, err := exec.Command("launchctl", "load", plistPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl load: %w\n%s", err, string(out))
	}
	return nil
}

func RestartService() error { return RestartLaunchd() }

func UninstallLaunchd() error {
	if !IsLaunchdInstalled() {
		return fmt.Errorf("launchd service not installed")
	}
	exec.Command("launchctl", "unload", LaunchdPlistPath()).Run()
	return os.Remove(LaunchdPlistPath())
}

func InstallService() (string, error) { return InstallLaunchd() }
func UninstallService() error         { return UninstallLaunchd() }
func IsServiceInstalled() bool        { return IsLaunchdInstalled() }
