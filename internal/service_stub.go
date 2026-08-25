//go:build !darwin && !linux

package internal

import "fmt"

func InstallService() (string, error) {
	return "", fmt.Errorf("service manager not supported on this platform")
}

func UninstallService() error {
	return fmt.Errorf("service manager not supported on this platform")
}

func RestartService() error {
	return fmt.Errorf("service manager not supported on this platform")
}

func IsServiceInstalled() bool {
	return false
}
