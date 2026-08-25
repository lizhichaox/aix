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
// launchd/systemd and the on-demand AIX gateway launcher.
func runInternalProxyService() error {
	cfg, err := internal.LoadProxyConfig()
	if err != nil {
		return fmt.Errorf("load AIX gateway config: %w", err)
	}
	if err := internal.WritePidFile(os.Getpid()); err != nil {
		return fmt.Errorf("write AIX gateway pid: %w", err)
	}
	defer internal.RemovePidFile()
	return internal.NewProxyServer(cfg).Start()
}

// startInternalProxy starts the AIX gateway without registering a public
// Cobra command. It is used when no system service has been installed yet.
func startInternalProxy() (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, fmt.Errorf("find executable: %w", err)
	}
	logFile, err := os.OpenFile(internal.ProxyLogPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return 0, fmt.Errorf("open AIX gateway log: %w", err)
	}
	defer logFile.Close()

	child := exec.Command(exe)
	child.Env = append(os.Environ(), internal.ProxyServiceEnv+"=1")
	child.Stdout = logFile
	child.Stderr = logFile
	child.Dir = "/"
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := child.Start(); err != nil {
		return 0, fmt.Errorf("start AIX gateway: %w", err)
	}
	pid := child.Process.Pid
	if err := child.Process.Release(); err != nil {
		return pid, fmt.Errorf("release AIX gateway process: %w", err)
	}
	return pid, nil
}

// ensureAIXGateway reloads the private gateway after provider/model
// changes. Installed service definitions are rewritten so upgrades migrate
// away from the removed `aix proxy` command automatically.
func ensureAIXGateway() error {
	if internal.IsServiceInstalled() {
		if _, err := internal.InstallService(); err != nil {
			return fmt.Errorf("refresh AIX gateway service: %w", err)
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
	consecutiveHealthy := 0
	for i := 0; i < 50; i++ {
		if _, err := internal.FetchProxyHealth(cfg.Listen); err == nil {
			consecutiveHealthy++
			// Do not launch a host app after observing only a transient process.
			// launchd reloads used to briefly expose an instance that was already
			// scheduled to stop, which made Codex exhaust its reconnect attempts.
			if consecutiveHealthy >= 5 {
				return nil
			}
		} else {
			lastErr = err
			consecutiveHealthy = 0
		}
		time.Sleep(100 * time.Millisecond)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("health endpoint did not remain stable")
	}
	return fmt.Errorf("AIX gateway did not become ready: %w (see aix log)", lastErr)
}
