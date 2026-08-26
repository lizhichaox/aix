package internal

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestParseCodexNativeUsage(t *testing.T) {
	raw := []byte(`{"rateLimits":{"planType":"pro","primary":{"usedPercent":37,"windowDurationMins":300,"resetsAt":1787731200},"secondary":{"usedPercent":72,"windowDurationMins":10080,"resetsAt":1788163200}}}`)
	usage, err := parseCodexUsage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Harness != HarnessCodex || usage.Plan != "pro" || len(usage.Windows) != 2 {
		t.Fatalf("usage = %+v", usage)
	}
	if usage.Windows[0].Name != "5-hour" || usage.Windows[0].RemainingPercent != 63 {
		t.Errorf("primary = %+v", usage.Windows[0])
	}
	if usage.Windows[1].Name != "weekly" || usage.Windows[1].RemainingPercent != 28 {
		t.Errorf("secondary = %+v", usage.Windows[1])
	}
}

func TestParseClaudeNativeUsage(t *testing.T) {
	raw := []byte(`{"five_hour":{"utilization":12.5,"resets_at":"2026-08-26T10:00:00Z"},"seven_day":{"utilization":80,"resets_at":"2026-09-01T10:00:00Z"},"extra_usage":[{"used":1}]}`)
	usage, err := parseClaudeUsage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Harness != HarnessClaude || len(usage.Windows) != 2 {
		t.Fatalf("usage = %+v", usage)
	}
	if usage.Windows[0].Name != "5-hour" || usage.Windows[0].RemainingPercent != 87.5 {
		t.Errorf("five-hour = %+v", usage.Windows[0])
	}
	if usage.Windows[1].ResetsAt == nil || !usage.Windows[1].ResetsAt.Equal(time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("weekly reset = %+v", usage.Windows[1])
	}
}

func TestClaudeCredentialMetadata(t *testing.T) {
	var payload struct {
		ClaudeAI *struct {
			AccessToken      string `json:"accessToken"`
			SubscriptionType string `json:"subscriptionType"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal([]byte(`{"claudeAiOauth":{"accessToken":"token","subscriptionType":"pro"}}`), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ClaudeAI == nil || payload.ClaudeAI.SubscriptionType != "pro" {
		t.Fatalf("credentials = %+v", payload.ClaudeAI)
	}
}

func TestClampPercent(t *testing.T) {
	if clampPercent(-1) != 0 || clampPercent(101) != 100 || clampPercent(42) != 42 {
		t.Fatal("clampPercent bounds failed")
	}
}

func TestCodexUsageAppServerArgsPinNativeAuth(t *testing.T) {
	args := codexUsageAppServerArgs()
	joined := strings.Join(args, " ")
	for _, want := range []string{"model_provider=openai", "preferred_auth_method=chatgpt", "forced_login_method=chatgpt"} {
		if !strings.Contains(joined, want) {
			t.Errorf("codexUsageAppServerArgs = %v, missing %q", args, want)
		}
	}
}

func TestNormalizeCodexUsageError(t *testing.T) {
	if got := normalizeCodexUsageError("codex account authentication required to read rate limits"); !strings.Contains(got, "codex login") {
		t.Errorf("normalizeCodexUsageError = %q, want login guidance", got)
	}
	if got := normalizeCodexUsageError("some other error"); got != "some other error" {
		t.Errorf("normalizeCodexUsageError = %q, want passthrough", got)
	}
}
