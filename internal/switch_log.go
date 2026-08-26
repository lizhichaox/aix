package internal

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// AppendSwitchLog records a completed harness selection in the gateway log.
// Keeping switch events beside request routes makes `aix log` a single audit
// trail even when no request has been sent through the new route yet.
func AppendSwitchLog(harness, provider, model, effort string) error {
	if err := EnsureDirs(); err != nil {
		return err
	}
	f, err := os.OpenFile(ProxyLogPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("open AIX gateway log: %w", err)
	}
	defer f.Close()

	_, err = fmt.Fprintf(f, "%s [%s] [aix] switch: provider=%s model=%s effort=%s\n",
		time.Now().Format("2006/01/02 15:04:05"),
		switchLogValue(harness), switchLogValue(provider), switchLogValue(model), switchLogValue(effort))
	if err != nil {
		return fmt.Errorf("write AIX switch log: %w", err)
	}
	return nil
}

func switchLogValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	return strings.Join(strings.Fields(value), "_")
}
