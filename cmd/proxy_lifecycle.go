package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/lizhichaox/aix/internal"
)

// runInternalProxyService is the private process entry point used by
// launchd/systemd and the on-demand Claude gateway launcher.
func runInternalProxyService() error {
	cfg, err := internal.LoadProxyConfig()
	if err != nil {
		return fmt.Errorf("load Claude gateway config: %w", err)
	}
	if err := internal.WritePidFile(os.Getpid()); err != nil {
		return fmt.Errorf("write Claude gateway pid: %w", err)
	}
	defer internal.RemovePidFile()
	return internal.NewProxyServer(cfg).Start()
}

// startInternalProxy starts the Claude gateway without registering a public
// Cobra command. It is used when no system service has been installed yet.
func startInternalProxy() (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, fmt.Errorf("find executable: %w", err)
	}
	logFile, err := os.OpenFile(internal.ProxyLogPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return 0, fmt.Errorf("open Claude gateway log: %w", err)
	}
	defer logFile.Close()

	child := exec.Command(exe)
	child.Env = append(os.Environ(), internal.ProxyServiceEnv+"=1")
	child.Stdout = logFile
	child.Stderr = logFile
	child.Dir = "/"
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := child.Start(); err != nil {
		return 0, fmt.Errorf("start Claude gateway: %w", err)
	}
	pid := child.Process.Pid
	if err := child.Process.Release(); err != nil {
		return pid, fmt.Errorf("release Claude gateway process: %w", err)
	}
	return pid, nil
}

// ensureClaudeGateway reloads the private gateway after provider/model
// changes. Installed service definitions are rewritten so upgrades migrate
// away from the removed `aix proxy` command automatically.
func ensureClaudeGateway() error {
	if internal.IsServiceInstalled() {
		if _, err := internal.InstallService(); err != nil {
			return fmt.Errorf("refresh Claude gateway service: %w", err)
		}
	} else {
		if running, pid := internal.IsProxyRunning(); running {
			process, err := os.FindProcess(pid)
			if err == nil {
				_ = process.Signal(syscall.SIGTERM)
				for i := 0; i < 20; i++ {
					if process.Signal(syscall.Signal(0)) != nil {
						break
					}
					time.Sleep(100 * time.Millisecond)
				}
			}
			internal.RemovePidFile()
		}
		if _, err := startInternalProxy(); err != nil {
			return err
		}
	}

	cfg, err := internal.LoadProxyConfig()
	if err != nil {
		return err
	}
	var lastErr error
	for i := 0; i < 30; i++ {
		if _, err := internal.FetchProxyHealth(cfg.Listen); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("Claude gateway did not become ready: %w (see aix log)", lastErr)
}
