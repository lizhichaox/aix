package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	UsageKindSubscription = "subscription_windows"
	UsageKindBalance      = "balance"
	UsageKindSpendLimit   = "spend_limit"
)

var supportedProviderUsage = []string{"opencode-go", "deepseek", "openrouter"}

type ProviderUsage struct {
	Provider  string         `json:"provider"`
	Kind      string         `json:"kind"`
	Available *bool          `json:"available,omitempty"`
	Balances  []UsageBalance `json:"balances,omitempty"`
	Windows   []UsageWindow  `json:"windows,omitempty"`
}

type UsageBalance struct {
	Currency string  `json:"currency"`
	Total    float64 `json:"total"`
	Granted  float64 `json:"granted,omitempty"`
	ToppedUp float64 `json:"topped_up,omitempty"`
}

// ConfiguredUsageProviders returns supported providers whose API key can be
// resolved. It never returns or persists the credential itself.
func ConfiguredUsageProviders() []string {
	configured := make([]string, 0, len(supportedProviderUsage))
	for _, provider := range supportedProviderUsage {
		if key, _ := NativeProviderAPIKey(provider); key != "" {
			configured = append(configured, provider)
		}
	}
	return configured
}

func ReadProviderUsage(ctx context.Context, provider string) (ProviderUsage, error) {
	key, _ := NativeProviderAPIKey(provider)
	if key == "" {
		return ProviderUsage{}, fmt.Errorf("%s API key is not configured", provider)
	}
	switch provider {
	case "opencode-go":
		return readOpenCodeGoUsage(ctx, key)
	case "deepseek":
		return readDeepSeekUsage(ctx, key)
	case "openrouter":
		return readOpenRouterUsage(ctx, key)
	default:
		return ProviderUsage{}, fmt.Errorf("provider %q does not support usage queries", provider)
	}
}

func getProviderUsage(ctx context.Context, endpoint, key string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("User-Agent", "aix/"+ProxyVersion)
	resp, err := nativeUsageHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		message := strings.TrimSpace(string(body))
		if len(message) > 512 {
			message = message[:512]
		}
		return nil, fmt.Errorf("usage endpoint returned %s: %s", resp.Status, message)
	}
	return body, nil
}

type openCodeGoResponse struct {
	Usage struct {
		Rolling *openCodeGoWindow `json:"rolling"`
		Weekly  *openCodeGoWindow `json:"weekly"`
		Monthly *openCodeGoWindow `json:"monthly"`
	} `json:"usage"`
}

type openCodeGoWindow struct {
	Percent  float64 `json:"percent"`
	ResetsAt string  `json:"resetsAt"`
}

func readOpenCodeGoUsage(ctx context.Context, key string) (ProviderUsage, error) {
	raw, err := getProviderUsage(ctx, "https://opencode.ai/zen/go/v1/usage", key)
	if err != nil {
		return ProviderUsage{}, fmt.Errorf("query OpenCode Go usage: %w", err)
	}
	return parseOpenCodeGoUsage(raw)
}

func parseOpenCodeGoUsage(raw []byte) (ProviderUsage, error) {
	var payload openCodeGoResponse
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ProviderUsage{}, fmt.Errorf("decode OpenCode Go usage: %w", err)
	}
	usage := ProviderUsage{Provider: "opencode-go", Kind: UsageKindSubscription}
	for _, candidate := range []struct {
		name   string
		window *openCodeGoWindow
	}{{"5-hour", payload.Usage.Rolling}, {"weekly", payload.Usage.Weekly}, {"monthly", payload.Usage.Monthly}} {
		if candidate.window == nil {
			continue
		}
		window := UsageWindow{Name: candidate.name, UsedPercent: candidate.window.Percent, RemainingPercent: clampPercent(100 - candidate.window.Percent)}
		if parsed, err := time.Parse(time.RFC3339, candidate.window.ResetsAt); err == nil {
			window.ResetsAt = &parsed
		}
		usage.Windows = append(usage.Windows, window)
	}
	return usage, nil
}

type deepSeekBalanceResponse struct {
	Available    bool `json:"is_available"`
	BalanceInfos []struct {
		Currency string `json:"currency"`
		Total    string `json:"total_balance"`
		Granted  string `json:"granted_balance"`
		ToppedUp string `json:"topped_up_balance"`
	} `json:"balance_infos"`
}

func readDeepSeekUsage(ctx context.Context, key string) (ProviderUsage, error) {
	raw, err := getProviderUsage(ctx, "https://api.deepseek.com/user/balance", key)
	if err != nil {
		return ProviderUsage{}, fmt.Errorf("query DeepSeek balance: %w", err)
	}
	return parseDeepSeekUsage(raw)
}

func parseDeepSeekUsage(raw []byte) (ProviderUsage, error) {
	var payload deepSeekBalanceResponse
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ProviderUsage{}, fmt.Errorf("decode DeepSeek balance: %w", err)
	}
	usage := ProviderUsage{Provider: "deepseek", Kind: UsageKindBalance, Available: &payload.Available}
	for _, item := range payload.BalanceInfos {
		total, err := strconv.ParseFloat(item.Total, 64)
		if err != nil {
			return ProviderUsage{}, fmt.Errorf("decode DeepSeek total balance: %w", err)
		}
		granted, err := strconv.ParseFloat(item.Granted, 64)
		if err != nil {
			return ProviderUsage{}, fmt.Errorf("decode DeepSeek granted balance: %w", err)
		}
		toppedUp, err := strconv.ParseFloat(item.ToppedUp, 64)
		if err != nil {
			return ProviderUsage{}, fmt.Errorf("decode DeepSeek topped-up balance: %w", err)
		}
		usage.Balances = append(usage.Balances, UsageBalance{Currency: item.Currency, Total: total, Granted: granted, ToppedUp: toppedUp})
	}
	return usage, nil
}

type openRouterKeyResponse struct {
	Data struct {
		Usage      float64  `json:"usage"`
		Daily      float64  `json:"usage_daily"`
		Weekly     float64  `json:"usage_weekly"`
		Monthly    float64  `json:"usage_monthly"`
		Limit      *float64 `json:"limit"`
		Remaining  *float64 `json:"limit_remaining"`
		LimitReset *string  `json:"limit_reset"`
	} `json:"data"`
}

func readOpenRouterUsage(ctx context.Context, key string) (ProviderUsage, error) {
	raw, err := getProviderUsage(ctx, "https://openrouter.ai/api/v1/key", key)
	if err != nil {
		return ProviderUsage{}, fmt.Errorf("query OpenRouter usage: %w", err)
	}
	return parseOpenRouterUsage(raw)
}

func parseOpenRouterUsage(raw []byte) (ProviderUsage, error) {
	var payload openRouterKeyResponse
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ProviderUsage{}, fmt.Errorf("decode OpenRouter usage: %w", err)
	}
	usage := ProviderUsage{Provider: "openrouter", Kind: UsageKindSpendLimit}
	periods := []struct {
		name string
		used float64
	}{{"daily", payload.Data.Daily}, {"weekly", payload.Data.Weekly}, {"monthly", payload.Data.Monthly}, {"lifetime", payload.Data.Usage}}
	for _, period := range periods {
		window := UsageWindow{Name: period.name, UsedAmount: floatPointer(period.used)}
		if payload.Data.LimitReset != nil && *payload.Data.LimitReset == period.name {
			window.LimitAmount = payload.Data.Limit
			window.RemainingAmount = payload.Data.Remaining
			window.ResetPolicy = *payload.Data.LimitReset
			if payload.Data.Limit != nil && *payload.Data.Limit > 0 {
				window.UsedPercent = clampPercent(period.used / *payload.Data.Limit * 100)
				window.RemainingPercent = clampPercent(100 - window.UsedPercent)
			}
		}
		usage.Windows = append(usage.Windows, window)
	}
	return usage, nil
}

func floatPointer(value float64) *float64 { return &value }
