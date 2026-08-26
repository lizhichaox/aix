package internal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// DefaultUsageCacheTTL is the default freshness window for provider-reported
// usage snapshots. Repeated queries within the window are served from disk
// instead of hitting the provider again, protecting against provider-side
// rate limits.
const DefaultUsageCacheTTL = 60 * time.Second

// UsageCacheRecord is a cached provider-reported usage snapshot. It never
// stores credentials and never computes or estimates usage.
type UsageCacheRecord struct {
	QueriedAt time.Time      `json:"queried_at"`
	Kind      string         `json:"kind"`
	Plan      string         `json:"plan,omitempty"`
	Available *bool          `json:"available,omitempty"`
	Balances  []UsageBalance `json:"balances,omitempty"`
	Windows   []UsageWindow  `json:"windows,omitempty"`
}

// UsageCachePath returns the on-disk usage cache location.
func UsageCachePath() string {
	return filepath.Join(AixDir(), "usage_cache.json")
}

// LoadUsageCache reads the cached usage snapshots. A missing or corrupt cache
// is treated as empty so a bad cache never blocks the command.
func LoadUsageCache() (map[string]*UsageCacheRecord, error) {
	entries := map[string]*UsageCacheRecord{}
	raw, err := os.ReadFile(UsageCachePath())
	if err != nil {
		if os.IsNotExist(err) {
			return entries, nil
		}
		return nil, err
	}
	if len(raw) == 0 {
		return entries, nil
	}
	if err := json.Unmarshal(raw, &entries); err != nil {
		return map[string]*UsageCacheRecord{}, nil
	}
	return entries, nil
}

// SaveUsageCache writes the usage cache atomically under restricted
// permissions.
func SaveUsageCache(entries map[string]*UsageCacheRecord) error {
	if err := os.MkdirAll(AixDir(), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	tmp := UsageCachePath() + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, UsageCachePath())
}

// UsageCacheRecordFresh reports whether a cached snapshot is still within the
// freshness window. A zero or negative TTL disables the cache entirely.
func UsageCacheRecordFresh(rec *UsageCacheRecord, ttl time.Duration, now time.Time) bool {
	if rec == nil || ttl <= 0 || rec.QueriedAt.IsZero() {
		return false
	}
	return now.Sub(rec.QueriedAt) < ttl
}
