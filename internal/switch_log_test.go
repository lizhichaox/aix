package internal

import (
	"os"
	"strings"
	"testing"
)

func TestAppendSwitchLogRecordsSelection(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := AppendSwitchLog("codex", "opencode-go", "gpt-5.6-luna", "high"); err != nil {
		t.Fatalf("AppendSwitchLog: %v", err)
	}
	raw, err := os.ReadFile(ProxyLogPath())
	if err != nil {
		t.Fatalf("read switch log: %v", err)
	}
	got := string(raw)
	want := "[codex] [aix] switch: provider=opencode-go model=gpt-5.6-luna effort=high"
	if !strings.Contains(got, want) {
		t.Fatalf("switch log = %q, want it to contain %q", got, want)
	}
}

func TestAppendSwitchLogUsesUnknownForMissingValues(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := AppendSwitchLog("claude", "custom", "", ""); err != nil {
		t.Fatalf("AppendSwitchLog: %v", err)
	}
	raw, err := os.ReadFile(ProxyLogPath())
	if err != nil {
		t.Fatalf("read switch log: %v", err)
	}
	if !strings.Contains(string(raw), "model=unknown effort=unknown") {
		t.Fatalf("missing values were not normalized: %q", raw)
	}
}
