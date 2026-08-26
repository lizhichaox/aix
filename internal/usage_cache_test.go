package internal

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUsageCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	now := time.Now().UTC().Truncate(time.Second)
	entries := map[string]*UsageCacheRecord{
		"codex": {QueriedAt: now, Kind: UsageKindSubscription, Plan: "plus", Windows: []UsageWindow{{Name: "5-hour", RemainingPercent: 92}}},
	}
	if err := SaveUsageCache(entries); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(UsageCachePath()); err != nil {
		t.Fatalf("cache not written: %v", err)
	}
	loaded, err := LoadUsageCache()
	if err != nil {
		t.Fatal(err)
	}
	rec := loaded["codex"]
	if rec == nil || rec.Plan != "plus" || rec.Kind != UsageKindSubscription || len(rec.Windows) != 1 {
		t.Fatalf("loaded = %+v", rec)
	}
	if !rec.QueriedAt.Equal(now) {
		t.Errorf("queried_at = %v, want %v", rec.QueriedAt, now)
	}
}

func TestUsageCacheIgnoreCorruptFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	path := UsageCachePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := LoadUsageCache()
	if err != nil {
		t.Fatalf("corrupt cache should not error: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("corrupt cache should be empty, got %d entries", len(entries))
	}
}

func TestUsageCacheRecordFresh(t *testing.T) {
	now := time.Now()
	fresh := &UsageCacheRecord{QueriedAt: now.Add(-30 * time.Second)}
	stale := &UsageCacheRecord{QueriedAt: now.Add(-90 * time.Second)}
	if !UsageCacheRecordFresh(fresh, 60*time.Second, now) {
		t.Error("fresh record should be fresh")
	}
	if UsageCacheRecordFresh(stale, 60*time.Second, now) {
		t.Error("stale record should not be fresh")
	}
	if UsageCacheRecordFresh(fresh, 0, now) {
		t.Error("zero ttl should disable cache")
	}
	if UsageCacheRecordFresh(nil, 60*time.Second, now) {
		t.Error("nil record should not be fresh")
	}
}
