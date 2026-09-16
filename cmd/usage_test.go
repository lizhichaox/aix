package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lizhichaox/aix/internal"
)

func TestUsageCurrencyPrefix(t *testing.T) {
	cases := map[string]string{"CNY": "¥", "usd": "$", "EUR": "EUR ", "": ""}
	for currency, want := range cases {
		if got := usageCurrencyPrefix(currency); got != want {
			t.Errorf("usageCurrencyPrefix(%q) = %q, want %q", currency, got, want)
		}
	}
}

func TestUsageFailureSupportsStrictAggregateMode(t *testing.T) {
	items := []usageItem{{Provider: "codex"}, {Provider: "deepseek", Error: "unavailable"}}
	if err := usageFailure(items, false); err != nil {
		t.Fatalf("non-strict aggregate returned %v", err)
	}
	err := usageFailure(items, true)
	if err == nil || !strings.Contains(err.Error(), "deepseek: unavailable") {
		t.Fatalf("strict aggregate error = %v", err)
	}
}

func TestCurrentProviderReturnsStateDecodeError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	if err := os.MkdirAll(filepath.Dir(internal.StatePath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(internal.StatePath(), []byte("invalid = ["), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := currentProviderFor(internal.HarnessClaude); err == nil {
		t.Fatal("currentProviderFor should report malformed state")
	}
}

func TestOpenRouterLimitedWindow(t *testing.T) {
	limit := 10.0
	windows := []internal.UsageWindow{{Name: "daily"}, {Name: "monthly", LimitAmount: &limit, ResetPolicy: "monthly"}}
	got := openRouterLimitedWindow(windows)
	if got == nil || got.Name != "monthly" || got.ResetPolicy != "monthly" {
		t.Fatalf("limited window = %+v", got)
	}
}
