package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/lizhichaox/aix/internal"
	"github.com/spf13/cobra"
)

var readNativeUsage = internal.ReadNativeUsage
var readProviderUsage = internal.ReadProviderUsage
var configuredUsageProviders = internal.ConfiguredUsageProviders

type usageItem struct {
	Provider        string                  `json:"provider"`
	Kind            string                  `json:"kind"`
	Plan            string                  `json:"plan,omitempty"`
	CurrentProvider string                  `json:"current_provider,omitempty"`
	Available       *bool                   `json:"available,omitempty"`
	Balances        []internal.UsageBalance `json:"balances,omitempty"`
	Windows         []internal.UsageWindow  `json:"windows,omitempty"`
	Error           string                  `json:"error,omitempty"`
	Cached          bool                    `json:"cached,omitempty"`
	QueriedAt       *time.Time              `json:"queried_at,omitempty"`
}

type usageReport struct {
	Items []usageItem `json:"items"`
}

var usageCmd = &cobra.Command{
	Use: "usage [provider]", Short: "Show provider-reported allowance and balances", Args: cobra.MaximumNArgs(1), RunE: runUsage,
}

func init() {
	usageCmd.Flags().Bool("json", false, "output JSON")
	usageCmd.Flags().Duration("ttl", internal.DefaultUsageCacheTTL, "cache freshness window; 0 disables caching")
	rootCmd.AddCommand(usageCmd)
}

func runUsage(cmd *cobra.Command, args []string) error {
	targets := []string{"codex", "claude"}
	if len(args) == 1 {
		targets = []string{strings.ToLower(args[0])}
	} else {
		targets = append(targets, configuredUsageProviders()...)
	}
	ttl, _ := cmd.Flags().GetDuration("ttl")
	cache, _ := internal.LoadUsageCache()
	now := time.Now()
	items := make([]usageItem, len(targets))
	var wg sync.WaitGroup
	var mu sync.Mutex
	var writes map[string]*internal.UsageCacheRecord
	for i, target := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if rec := cache[target]; internal.UsageCacheRecordFresh(rec, ttl, now) {
				items[i] = usageItemFromCache(target, rec)
				return
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()
			item, rec := queryUsageItem(ctx, target)
			items[i] = item
			if rec != nil {
				mu.Lock()
				if writes == nil {
					writes = map[string]*internal.UsageCacheRecord{}
				}
				writes[target] = rec
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(writes) > 0 {
		merged, _ := internal.LoadUsageCache()
		if merged == nil {
			merged = map[string]*internal.UsageCacheRecord{}
		}
		for key, rec := range writes {
			merged[key] = rec
		}
		_ = internal.SaveUsageCache(merged)
	}
	jsonOut, _ := cmd.Flags().GetBool("json")
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(usageReport{Items: items})
	}
	for i, item := range items {
		if i > 0 {
			fmt.Println()
		}
		printUsageItem(item)
	}
	if len(args) == 1 && items[0].Error != "" {
		return fmt.Errorf("%s", items[0].Error)
	}
	return nil
}

func queryUsageItem(ctx context.Context, target string) (usageItem, *internal.UsageCacheRecord) {
	if target == internal.HarnessCodex || target == internal.HarnessClaude {
		usage, err := readNativeUsage(ctx, target)
		if err != nil {
			return usageItem{Provider: target, Kind: internal.UsageKindSubscription, Error: err.Error()}, nil
		}
		rec := &internal.UsageCacheRecord{QueriedAt: time.Now().UTC(), Kind: internal.UsageKindSubscription, Plan: usage.Plan, Windows: usage.Windows}
		item := usageItemFromUsage(target, internal.UsageKindSubscription, usage.Plan, nil, nil, usage.Windows)
		item.QueriedAt = &rec.QueriedAt
		return item, rec
	}
	usage, err := readProviderUsage(ctx, target)
	if err != nil {
		return usageItem{Provider: target, Error: err.Error()}, nil
	}
	rec := &internal.UsageCacheRecord{QueriedAt: time.Now().UTC(), Kind: usage.Kind, Available: usage.Available, Balances: usage.Balances, Windows: usage.Windows}
	item := usageItemFromUsage(usage.Provider, usage.Kind, "", usage.Available, usage.Balances, usage.Windows)
	item.QueriedAt = &rec.QueriedAt
	return item, rec
}

func usageItemFromCache(target string, rec *internal.UsageCacheRecord) usageItem {
	item := usageItemFromUsage(target, rec.Kind, rec.Plan, rec.Available, rec.Balances, rec.Windows)
	item.Cached = true
	item.QueriedAt = &rec.QueriedAt
	return item
}

func usageItemFromUsage(provider, kind, plan string, available *bool, balances []internal.UsageBalance, windows []internal.UsageWindow) usageItem {
	item := usageItem{
		Provider:  provider,
		Kind:      kind,
		Plan:      plan,
		Available: available,
		Balances:  balances,
		Windows:   windows,
	}
	if provider == internal.HarnessCodex || provider == internal.HarnessClaude {
		item.CurrentProvider = currentProviderFor(provider)
	}
	return item
}

func currentProviderFor(target string) string {
	state, _ := internal.LoadState()
	if target == internal.HarnessCodex {
		return buildCodexStatus(state).Provider
	}
	return buildClaudeStatus(state).Provider
}

func printUsageItem(item usageItem) {
	title := item.Provider
	if item.Plan != "" {
		title += " (" + item.Plan + ")"
	}
	if item.CurrentProvider != "" {
		title += " | Current provider: " + item.CurrentProvider
	}
	if item.Cached {
		title += " (cached)"
	}
	fmt.Println(title)
	if item.Error != "" {
		fmt.Printf("  Error: %s\n", item.Error)
		return
	}
	if len(item.Balances) > 0 {
		for _, balance := range item.Balances {
			fmt.Printf("  Balance: %s%.2f\n", usageCurrencyPrefix(balance.Currency), balance.Total)
		}
	}
	if item.Provider == "openrouter" {
		printOpenRouterUsage(item.Windows)
		return
	}
	if len(item.Windows) > 0 {
		printRemainingWindows(item.Windows)
	}
}

func printRemainingWindows(windows []internal.UsageWindow) {
	if len(windows) == 0 {
		fmt.Println("  No allowance was reported.")
		return
	}
	fmt.Printf("  %-16s %-11s %s\n", "Window", "Remaining", "Resets")
	for _, window := range windows {
		reset := "not reported"
		if window.ResetsAt != nil {
			reset = window.ResetsAt.Local().Format("Jan 2 15:04 MST")
		}
		fmt.Printf("  %-16s %-11s %s\n", window.Name, fmt.Sprintf("%.0f%%", window.RemainingPercent), reset)
	}
}

func printOpenRouterUsage(windows []internal.UsageWindow) {
	if limited := openRouterLimitedWindow(windows); limited != nil {
		label := limited.Name
		if label != "" {
			label = strings.ToUpper(label[:1]) + label[1:]
		}
		fmt.Printf("  %s limit: $%.2f\n", label, *limited.LimitAmount)
		if limited.RemainingAmount != nil {
			fmt.Printf("  Remaining: $%.2f\n", *limited.RemainingAmount)
		}
		if limited.ResetPolicy != "" {
			fmt.Printf("  Resets: %s\n", limited.ResetPolicy)
		}
		return
	}
	if len(windows) > 0 {
		fmt.Println("  Remaining: unlimited")
		return
	}
	fmt.Println("  No spending information was reported.")
}

func openRouterLimitedWindow(windows []internal.UsageWindow) *internal.UsageWindow {
	for i := range windows {
		if windows[i].LimitAmount != nil {
			return &windows[i]
		}
	}
	return nil
}

func usageCurrencyPrefix(currency string) string {
	switch strings.ToUpper(currency) {
	case "CNY":
		return "¥"
	case "USD":
		return "$"
	default:
		if currency == "" {
			return ""
		}
		return strings.ToUpper(currency) + " "
	}
}
