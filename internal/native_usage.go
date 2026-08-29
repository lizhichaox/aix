package internal

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// NativeUsage is a provider-reported snapshot. AIX never computes or estimates
// it; a short-lived cache may re-serve the last reported snapshot.
type NativeUsage struct {
	Harness         string        `json:"harness"`
	CurrentProvider string        `json:"current_provider,omitempty"`
	Plan            string        `json:"plan,omitempty"`
	Windows         []UsageWindow `json:"windows"`
}

type UsageWindow struct {
	Name             string     `json:"name"`
	UsedPercent      float64    `json:"used_percent"`
	RemainingPercent float64    `json:"remaining_percent"`
	UsedAmount       *float64   `json:"used_amount,omitempty"`
	LimitAmount      *float64   `json:"limit_amount,omitempty"`
	RemainingAmount  *float64   `json:"remaining_amount,omitempty"`
	ResetPolicy      string     `json:"reset_policy,omitempty"`
	ResetsAt         *time.Time `json:"resets_at,omitempty"`
	DurationMinutes  int64      `json:"duration_minutes,omitempty"`
}

var nativeUsageCommand = exec.CommandContext
var nativeUsageHTTPClient = http.DefaultClient

func ReadNativeUsage(ctx context.Context, harness string) (NativeUsage, error) {
	switch harness {
	case HarnessCodex:
		return readCodexNativeUsage(ctx)
	case HarnessClaude:
		return readClaudeNativeUsage(ctx)
	default:
		return NativeUsage{}, fmt.Errorf("unsupported harness %q", harness)
	}
}

type codexRPCResponse struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type codexRateLimits struct {
	RateLimits struct {
		PlanType  string            `json:"planType"`
		Primary   *codexUsageWindow `json:"primary"`
		Secondary *codexUsageWindow `json:"secondary"`
	} `json:"rateLimits"`
}

type codexUsageWindow struct {
	UsedPercent        float64 `json:"usedPercent"`
	ResetsAt           *int64  `json:"resetsAt"`
	WindowDurationMins *int64  `json:"windowDurationMins"`
}

func readCodexNativeUsage(ctx context.Context) (NativeUsage, error) {
	args := codexUsageAppServerArgs()
	cmd := nativeUsageCommand(ctx, "codex", args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return NativeUsage{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return NativeUsage{}, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return NativeUsage{}, fmt.Errorf("start Codex app server: %w", err)
	}
	enc := json.NewEncoder(stdin)
	requests := []any{
		map[string]any{"id": 1, "method": "initialize", "params": map[string]any{"clientInfo": map[string]string{"name": "aix", "version": ProxyVersion}, "capabilities": map[string]bool{"experimentalApi": true}}},
		map[string]any{"method": "initialized"},
		map[string]any{"id": 2, "method": "account/rateLimits/read"},
	}
	for _, request := range requests {
		if err := enc.Encode(request); err != nil {
			_ = cmd.Process.Kill()
			return NativeUsage{}, err
		}
	}
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		var response codexRPCResponse
		if json.Unmarshal(scanner.Bytes(), &response) != nil || response.ID != 2 {
			continue
		}
		_ = cmd.Process.Kill()
		_ = stdin.Close()
		_ = cmd.Wait()
		if response.Error != nil {
			return NativeUsage{}, fmt.Errorf("Codex native usage: %s", normalizeCodexUsageError(response.Error.Message))
		}
		return parseCodexUsage(response.Result)
	}
	_ = stdin.Close()
	_ = cmd.Wait()
	if err := scanner.Err(); err != nil {
		return NativeUsage{}, err
	}
	message := strings.TrimSpace(stderr.String())
	if message == "" {
		message = "no response from Codex app server"
	}
	return NativeUsage{}, errors.New(message)
}

// codexUsageAppServerArgs returns the args used to read native rate limits.
// Reading account rate limits requires a ChatGPT-account (chatgpt) login. The
// live config may be switched to a managed third-party provider that forces
// API-key auth, so these overrides pin the subprocess to the native account
// context without modifying the on-disk configuration.
func codexUsageAppServerArgs() []string {
	return []string{
		"app-server",
		"-c", "model_provider=openai",
		"-c", "preferred_auth_method=chatgpt",
		"-c", "forced_login_method=chatgpt",
		"--listen", "stdio://",
	}
}

// normalizeCodexUsageError turns a terse app-server error into actionable
// guidance for the common "not authenticated" case.
func normalizeCodexUsageError(message string) string {
	lower := strings.ToLower(message)
	if strings.Contains(lower, "account authentication required") ||
		strings.Contains(lower, "chatgpt account") ||
		strings.Contains(lower, "not logged in") {
		return "native ChatGPT account login is required; run `codex login` and sign in with ChatGPT"
	}
	return message
}

func parseCodexUsage(raw []byte) (NativeUsage, error) {
	var snapshot codexRateLimits
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return NativeUsage{}, fmt.Errorf("decode Codex usage: %w", err)
	}
	usage := NativeUsage{Harness: HarnessCodex, Plan: snapshot.RateLimits.PlanType}
	for _, window := range []*codexUsageWindow{snapshot.RateLimits.Primary, snapshot.RateLimits.Secondary} {
		if window != nil {
			usage.Windows = append(usage.Windows, codexWindow(window))
		}
	}
	sort.SliceStable(usage.Windows, func(i, j int) bool { return usage.Windows[i].DurationMinutes < usage.Windows[j].DurationMinutes })
	return usage, nil
}

func codexWindow(window *codexUsageWindow) UsageWindow {
	duration := int64(0)
	if window.WindowDurationMins != nil {
		duration = *window.WindowDurationMins
	}
	result := UsageWindow{Name: usageWindowName(duration), UsedPercent: window.UsedPercent, RemainingPercent: clampPercent(100 - window.UsedPercent), DurationMinutes: duration}
	if window.ResetsAt != nil {
		t := time.Unix(*window.ResetsAt, 0)
		result.ResetsAt = &t
	}
	return result
}

type claudeUsageWindow struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    string  `json:"resets_at"`
}

type claudeUsageResponse struct {
	FiveHour       *claudeUsageWindow `json:"five_hour"`
	SevenDay       *claudeUsageWindow `json:"seven_day"`
	SevenDaySonnet *claudeUsageWindow `json:"seven_day_sonnet"`
	SevenDayOpus   *claudeUsageWindow `json:"seven_day_opus"`
}

func readClaudeNativeUsage(ctx context.Context) (NativeUsage, error) {
	credentials, err := readClaudeOAuthCredentialCandidates(ctx)
	if err == nil {
		for _, candidate := range credentials {
			usage, rejected, queryErr := queryClaudeUsage(ctx, candidate)
			if queryErr == nil {
				return usage, nil
			}
			if !rejected {
				return NativeUsage{}, queryErr
			}
		}
	}
	if usage, snapshotErr := readClaudeDesktopUsageSnapshot(time.Now()); snapshotErr == nil {
		return usage, nil
	}
	if err != nil {
		return NativeUsage{}, err
	}
	return NativeUsage{}, errors.New("Claude native logins are expired or unauthorized; sign in to Claude Code or Claude Desktop again")
}

const claudeDesktopUsageMaxAge = time.Hour

func readClaudeDesktopUsageSnapshot(now time.Time) (NativeUsage, error) {
	path := filepath.Join(HomeDir(), "Library", "Application Support", "Claude", "plan-usage-history.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return NativeUsage{}, err
	}
	return parseClaudeDesktopUsageSnapshot(raw, now)
}

func parseClaudeDesktopUsageSnapshot(raw []byte, now time.Time) (NativeUsage, error) {
	var history struct {
		Samples []struct {
			TimestampMS int64 `json:"t"`
			Usage       struct {
				FiveHour *float64 `json:"fh"`
				SevenDay *float64 `json:"sd"`
			} `json:"u"`
		} `json:"samples"`
	}
	if err := json.Unmarshal(raw, &history); err != nil {
		return NativeUsage{}, fmt.Errorf("decode Claude Desktop usage history: %w", err)
	}
	if len(history.Samples) == 0 {
		return NativeUsage{}, errors.New("Claude Desktop usage history is empty")
	}
	latest := history.Samples[len(history.Samples)-1]
	reportedAt := time.UnixMilli(latest.TimestampMS)
	age := now.Sub(reportedAt)
	if latest.TimestampMS <= 0 || age > claudeDesktopUsageMaxAge || age < -5*time.Minute {
		return NativeUsage{}, errors.New("Claude Desktop usage snapshot is stale")
	}
	usage := NativeUsage{Harness: HarnessClaude}
	for _, candidate := range []struct {
		name  string
		value *float64
	}{
		{"5-hour", latest.Usage.FiveHour},
		{"weekly", latest.Usage.SevenDay},
	} {
		if candidate.value != nil {
			usage.Windows = append(usage.Windows, UsageWindow{
				Name:             candidate.name,
				UsedPercent:      *candidate.value,
				RemainingPercent: clampPercent(100 - *candidate.value),
			})
		}
	}
	if len(usage.Windows) == 0 {
		return NativeUsage{}, errors.New("Claude Desktop usage snapshot has no usage windows")
	}
	return usage, nil
}

func queryClaudeUsage(ctx context.Context, credentials claudeOAuthCredentials) (NativeUsage, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.anthropic.com/api/oauth/usage", nil)
	if err != nil {
		return NativeUsage{}, false, err
	}
	req.Header.Set("Authorization", "Bearer "+credentials.AccessToken)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("User-Agent", "aix/"+ProxyVersion)
	resp, err := nativeUsageHTTPClient.Do(req)
	if err != nil {
		return NativeUsage{}, false, fmt.Errorf("query Claude native usage: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return NativeUsage{}, true, errors.New("Claude OAuth credential rejected")
		}
		return NativeUsage{}, false, fmt.Errorf("Claude usage query returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return NativeUsage{}, false, err
	}
	usage, err := parseClaudeUsage(raw)
	if err != nil {
		return NativeUsage{}, false, err
	}
	usage.Plan = credentials.SubscriptionType
	return usage, false, nil
}

func parseClaudeUsage(raw []byte) (NativeUsage, error) {
	var payload claudeUsageResponse
	if err := json.Unmarshal(raw, &payload); err != nil {
		return NativeUsage{}, fmt.Errorf("decode Claude usage: %w", err)
	}
	usage := NativeUsage{Harness: HarnessClaude}
	windows := []struct {
		name   string
		window *claudeUsageWindow
	}{
		{"5-hour", payload.FiveHour},
		{"weekly", payload.SevenDay},
		{"weekly-sonnet", payload.SevenDaySonnet},
		{"weekly-opus", payload.SevenDayOpus},
	}
	for _, candidate := range windows {
		window := candidate.window
		if window == nil {
			continue
		}
		item := UsageWindow{Name: candidate.name, UsedPercent: window.Utilization, RemainingPercent: clampPercent(100 - window.Utilization)}
		if parsed, err := time.Parse(time.RFC3339, window.ResetsAt); err == nil {
			item.ResetsAt = &parsed
		}
		usage.Windows = append(usage.Windows, item)
	}
	return usage, nil
}

type claudeOAuthCredentials struct {
	AccessToken      string
	SubscriptionType string
}

func readClaudeOAuthCredentialCandidates(ctx context.Context) ([]claudeOAuthCredentials, error) {
	var candidates []claudeOAuthCredentials
	seen := make(map[string]bool)
	add := func(raw []byte) {
		for _, candidate := range parseClaudeOAuthCredentialCandidates(raw) {
			if candidate.AccessToken != "" && !seen[candidate.AccessToken] {
				seen[candidate.AccessToken] = true
				candidates = append(candidates, candidate)
			}
		}
	}
	if runtime.GOOS == "darwin" {
		for _, service := range []string{"Claude Code-credentials", "Claude Code"} {
			out, err := nativeUsageCommand(ctx, "security", "find-generic-password", "-s", service, "-w").Output()
			if err == nil && len(bytes.TrimSpace(out)) > 0 {
				add(bytes.TrimSpace(out))
			}
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	for _, path := range []string{
		home + "/.claude/.credentials.json",
		home + "/.config/claude/.credentials.json",
		ClaudeDesktopConfigPath(),
		NativeDesktopSnapPath(),
	} {
		if data, err := os.ReadFile(path); err == nil {
			add(data)
		}
	}
	if len(candidates) == 0 {
		return nil, errors.New("Claude native login not found; sign in to Claude Code or Claude Desktop")
	}
	return candidates, nil
}

func parseClaudeOAuthCredentialCandidates(raw []byte) []claudeOAuthCredentials {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	var candidates []claudeOAuthCredentials
	var collect func(any)
	collect = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			token, _ := typed["accessToken"].(string)
			if strings.TrimSpace(token) != "" {
				subscription, _ := typed["subscriptionType"].(string)
				candidates = append(candidates, claudeOAuthCredentials{AccessToken: strings.TrimSpace(token), SubscriptionType: subscription})
			}
		case []any:
			for _, child := range typed {
				collect(child)
			}
		}
	}
	root, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	collect(root)
	for _, key := range []string{"claudeAiOauth", "oauthTokens"} {
		if nested, exists := root[key]; exists {
			collect(nested)
		}
	}
	return candidates
}

func usageWindowName(minutes int64) string {
	switch minutes {
	case 300:
		return "5-hour"
	case 10080:
		return "weekly"
	case 1440:
		return "daily"
	default:
		if minutes > 0 {
			return fmt.Sprintf("%d-minute", minutes)
		}
		return "unknown"
	}
}

func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}
