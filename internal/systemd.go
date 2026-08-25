//go:build linux

package internal

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"text/template"
)

const systemdUnitName = "aix-proxy"

var systemdTmpl = template.Must(template.New("unit").Parse(`[Unit]
Description=AIX Claude gateway
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
Environment=AIX_INTERNAL_PROXY=1
ExecStart={{.Binary}}
Restart=always
RestartSec=2
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=default.target
`))

func systemdUnitPath() string {
	return filepath.Join(HomeDir(), ".config", "systemd", "user", systemdUnitName+".service")
}

func InstallSystemd() (string, error) {
	binary, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("cannot find aix binary: %w", err)
	}
	binary, _ = filepath.Abs(binary)
	binary, _ = filepath.EvalSymlinks(binary)

	// Stop any existing proxy
	if IsSystemdEnabled() {
		exec.Command("systemctl", "--user", "stop", systemdUnitName).Run()
	}

	dir := filepath.Dir(systemdUnitPath())
	os.MkdirAll(dir, 0755)

	f, err := os.Create(systemdUnitPath())
	if err != nil {
		return "", fmt.Errorf("create unit file: %w", err)
	}

	data := struct{ Binary string }{binary}
	if err := systemdTmpl.Execute(f, data); err != nil {
		f.Close()
		return "", fmt.Errorf("write unit: %w", err)
	}
	f.Close()

	// Reload and enable
	exec.Command("systemctl", "--user", "daemon-reload").Run()
	out, err := exec.Command("systemctl", "--user", "enable", "--now", systemdUnitName).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("systemctl enable: %w\n%s", err, string(out))
	}

	return binary, nil
}

func RestartSystemd() error {
	if !IsSystemdInstalled() {
		return fmt.Errorf("Claude gateway service is not installed; run aix setup")
	}
	exec.Command("systemctl", "--user", "daemon-reload").Run()
	out, err := exec.Command("systemctl", "--user", "restart", systemdUnitName).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl restart: %w\n%s", err, string(out))
	}
	return nil
}

func RestartService() error { return RestartSystemd() }

func UninstallSystemd() error {
	exec.Command("systemctl", "--user", "stop", systemdUnitName).Run()
	exec.Command("systemctl", "--user", "disable", systemdUnitName).Run()
	os.Remove(systemdUnitPath())
	exec.Command("systemctl", "--user", "daemon-reload").Run()
	return nil
}

func IsSystemdInstalled() bool {
	_, err := os.Stat(systemdUnitPath())
	return err == nil
}

func IsSystemdEnabled() bool {
	err := exec.Command("systemctl", "--user", "is-enabled", systemdUnitName).Run()
	return err == nil
}

func InstallService() (string, error) { return InstallSystemd() }
func UninstallService() error         { return UninstallSystemd() }
func IsServiceInstalled() bool        { return IsSystemdInstalled() }
