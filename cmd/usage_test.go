package cmd

import (
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

func TestOpenRouterLimitedWindow(t *testing.T) {
	limit := 10.0
	windows := []internal.UsageWindow{{Name: "daily"}, {Name: "monthly", LimitAmount: &limit, ResetPolicy: "monthly"}}
	got := openRouterLimitedWindow(windows)
	if got == nil || got.Name != "monthly" || got.ResetPolicy != "monthly" {
		t.Fatalf("limited window = %+v", got)
	}
}
