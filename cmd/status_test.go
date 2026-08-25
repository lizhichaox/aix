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
	status := harnessStatus{Effort: "high"}
	out, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "detail") || !strings.Contains(string(out), `"effort":"high"`) {
		t.Fatalf("status fields = %s, want effort without detail", out)
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
}

func TestNativeProviderEffortFallsBackToHarnessDefault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if got := statusEffort(internal.HarnessCodex, "openai"); got != internal.DefaultHarnessEffort {
		t.Errorf("native Codex effort = %q, want %q", got, internal.DefaultHarnessEffort)
	}
}
