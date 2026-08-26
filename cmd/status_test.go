package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lizhichaox/aix/internal"
)

func TestStatusJSONDoesNotExposeUsage(t *testing.T) {
	out, err := json.Marshal(statusData{Harnesses: []harnessStatus{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{"usage", "today", "requests", "tokens", "cost"} {
		if strings.Contains(strings.ToLower(string(out)), removed) {
			t.Errorf("status JSON contains removed usage field %q: %s", removed, out)
		}
	}
}

func TestStatusUsesHarnessColumns(t *testing.T) {
	status := harnessStatus{Model: "deepseek-v4-pro", Context: 1048576, Effort: "high"}
	out, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"model":"deepseek-v4-pro"`, `"context_length":1048576`, `"effort":"high"`} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("status fields = %s, missing %s", out, want)
		}
	}
	if strings.Contains(string(out), "detail") {
		t.Fatalf("status fields = %s, want no detail", out)
	}
}

func TestStatusHasOneRowPerHarness(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	statuses := buildHarnessStatuses(&internal.State{Apps: map[string]string{}})
	if len(statuses) != 2 {
		t.Fatalf("harness rows = %d, want 2", len(statuses))
	}
	if statuses[0].ID != internal.HarnessClaude || statuses[0].Name != "Claude" {
		t.Errorf("first harness = %+v, want Claude", statuses[0])
	}
	if statuses[0].Provider != "anthropic" || statuses[0].Effort != internal.DefaultHarnessEffort {
		t.Errorf("native Claude status = %+v, want anthropic/%s", statuses[0], internal.DefaultHarnessEffort)
	}
	if statuses[1].ID != internal.HarnessCodex || statuses[1].Name != "Codex" {
		t.Errorf("second harness = %+v, want Codex", statuses[1])
	}
	if statuses[1].Provider != "openai" || statuses[1].Mode != "native" || statuses[1].Effort != internal.DefaultHarnessEffort {
		t.Errorf("native Codex status = %+v, want openai/native/%s", statuses[1], internal.DefaultHarnessEffort)
	}
}

func TestNativeProviderEffortFallsBackToHarnessDefault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if got := statusEffort(internal.HarnessCodex, "openai"); got != internal.DefaultHarnessEffort {
		t.Errorf("native Codex effort = %q, want %q", got, internal.DefaultHarnessEffort)
	}
}

func TestFormatContextLength(t *testing.T) {
	cases := map[int]string{
		0:       "unknown",
		1048576: "1M",
		262144:  "256K",
		12345:   "12345",
	}
	for context, want := range cases {
		if got := formatContextLength(context); got != want {
			t.Errorf("formatContextLength(%d) = %q, want %q", context, got, want)
		}
	}
}

func TestStatusJSONOmitsUnknownContextLength(t *testing.T) {
	raw, err := json.Marshal(harnessStatus{ID: "claude", Name: "Claude", Provider: "anthropic", Mode: "native", Model: "sonnet"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "context_length") {
		t.Fatalf("unknown context_length should be omitted: %s", raw)
	}
}

func TestAnyManagedIncludesCodexGateway(t *testing.T) {
	if !anyManaged([]harnessStatus{{ID: internal.HarnessCodex, Provider: "opencode-go", Mode: "gateway"}}) {
		t.Error("managed Codex must require gateway health")
	}
	if anyManaged([]harnessStatus{{ID: internal.HarnessCodex, Provider: "openai", Mode: "responses"}}) {
		t.Error("native Codex must not require gateway health")
	}
}
