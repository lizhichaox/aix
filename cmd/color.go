package cmd

import (
	"os"
)

// isTerminal reports whether stdout is a terminal (for color output).
func isTerminal() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func colorGreen(s string) string {
	if !isTerminal() {
		return s
	}
	return "\033[32m" + s + "\033[0m"
}

func colorRed(s string) string {
	if !isTerminal() {
		return s
	}
	return "\033[31m" + s + "\033[0m"
}

func checkMark(pass bool) string {
	if pass {
		return colorGreen("✓")
	}
	return colorRed("✗")
}
