package internal

import (
	"testing"
	"time"
)

func TestParseOpenCodeGoUsage(t *testing.T) {
	raw := []byte(`{"usage":{"rolling":{"status":"ok","percent":9,"resetsAt":"2026-08-26T16:00:00Z"},"weekly":{"status":"ok","percent":34,"resetsAt":"2026-09-01T00:00:00Z"},"monthly":{"status":"ok","percent":56,"resetsAt":"2026-09-15T00:00:00Z"}}}`)
	usage, err := parseOpenCodeGoUsage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Provider != "opencode-go" || usage.Kind != UsageKindSubscription || len(usage.Windows) != 3 {
		t.Fatalf("usage = %+v", usage)
	}
	if usage.Windows[0].Name != "5-hour" || usage.Windows[0].RemainingPercent != 91 {
		t.Errorf("rolling = %+v", usage.Windows[0])
	}
	if usage.Windows[1].ResetsAt == nil || !usage.Windows[1].ResetsAt.Equal(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("weekly = %+v", usage.Windows[1])
	}
}

func TestParseDeepSeekUsage(t *testing.T) {
	raw := []byte(`{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"110.00","granted_balance":"10.00","topped_up_balance":"100.00"}]}`)
	usage, err := parseDeepSeekUsage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Available == nil || !*usage.Available || len(usage.Balances) != 1 {
		t.Fatalf("usage = %+v", usage)
	}
	if got := usage.Balances[0]; got.Currency != "CNY" || got.Total != 110 || got.Granted != 10 || got.ToppedUp != 100 {
		t.Errorf("balance = %+v", got)
	}
}

func TestParseOpenRouterUsage(t *testing.T) {
	raw := []byte(`{"data":{"usage":25.5,"usage_daily":1.5,"usage_weekly":8.5,"usage_monthly":25.5,"limit":100,"limit_remaining":74.5,"limit_reset":"monthly"}}`)
	usage, err := parseOpenRouterUsage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(usage.Windows) != 4 {
		t.Fatalf("windows = %+v", usage.Windows)
	}
	monthly := usage.Windows[2]
	if monthly.Name != "monthly" || monthly.UsedAmount == nil || *monthly.UsedAmount != 25.5 || monthly.RemainingAmount == nil || *monthly.RemainingAmount != 74.5 || monthly.RemainingPercent != 74.5 {
		t.Errorf("monthly = %+v", monthly)
	}
}
