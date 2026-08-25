package cmd

import (
	"strings"
	"testing"
)

func TestFormatLogRoute(t *testing.T) {
	status := harnessStatus{
		ID:       "claude",
		Provider: "opencode-go",
		Model:    "deepseek-v4-flash-vision-exp",
		Effort:   "high",
	}
	want := "Current route: harness=claude → provider=opencode-go → model=deepseek-v4-flash-vision-exp → effort=high"
	if got := formatLogRoute(status); got != want {
		t.Fatalf("formatLogRoute() = %q, want %q", got, want)
	}
}

func TestFormatLogRoutesIncludesBothHarnesses(t *testing.T) {
	got := formatLogRoutes([]harnessStatus{
		{ID: "claude", Provider: "opencode-go", Model: "deepseek-v4-pro", Effort: "high"},
		{ID: "codex", Provider: "openrouter", Model: "moonshotai/kimi-k2.5", Effort: "medium"},
	})
	for _, want := range []string{"Current routes:", "harness=claude", "harness=codex", "provider=openrouter", "effort=medium"} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatLogRoutes() missing %q: %s", want, got)
		}
	}
}

func TestFormatLogRouteMarksUnknownFields(t *testing.T) {
	want := "Current route: harness=unknown → provider=unknown → model=unknown → effort=unknown"
	if got := formatLogRoute(harnessStatus{}); got != want {
		t.Fatalf("formatLogRoute() = %q, want %q", got, want)
	}
}
