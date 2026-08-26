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
	credentials, err := readClaudeOAuthCredentials(ctx)
	if err != nil {
		return NativeUsage{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.anthropic.com/api/oauth/usage", nil)
	if err != nil {
		return NativeUsage{}, err
	}
	req.Header.Set("Authorization", "Bearer "+credentials.AccessToken)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("User-Agent", "aix/"+ProxyVersion)
	resp, err := nativeUsageHTTPClient.Do(req)
	if err != nil {
		return NativeUsage{}, fmt.Errorf("query Claude native usage: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return NativeUsage{}, errors.New("Claude native login expired or unauthorized; run `claude auth login` and sign in again")
		}
		return NativeUsage{}, fmt.Errorf("Claude usage query returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return NativeUsage{}, err
	}
	usage, err := parseClaudeUsage(raw)
	if err != nil {
		return NativeUsage{}, err
	}
	usage.Plan = credentials.SubscriptionType
	return usage, nil
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

func readClaudeOAuthCredentials(ctx context.Context) (claudeOAuthCredentials, error) {
	var raw []byte
	if runtime.GOOS == "darwin" {
		for _, service := range []string{"Claude Code-credentials", "Claude Code"} {
			out, err := nativeUsageCommand(ctx, "security", "find-generic-password", "-s", service, "-w").Output()
			if err == nil && len(bytes.TrimSpace(out)) > 0 {
				raw = bytes.TrimSpace(out)
				break
			}
		}
	}
	if len(raw) == 0 {
		home, err := os.UserHomeDir()
		if err != nil {
			return claudeOAuthCredentials{}, err
		}
		for _, path := range []string{home + "/.claude/.credentials.json", home + "/.config/claude/.credentials.json"} {
			if data, err := os.ReadFile(path); err == nil {
				raw = data
				break
			}
		}
	}
	if len(raw) == 0 {
		return claudeOAuthCredentials{}, errors.New("Claude native login not found; run `claude auth login`")
	}
	var credentials struct {
		ClaudeAI *struct {
			AccessToken      string `json:"accessToken"`
			SubscriptionType string `json:"subscriptionType"`
		} `json:"claudeAiOauth"`
		AccessToken      string `json:"accessToken"`
		SubscriptionType string `json:"subscriptionType"`
	}
	if err := json.Unmarshal(raw, &credentials); err != nil {
		return claudeOAuthCredentials{}, fmt.Errorf("decode Claude native credentials: %w", err)
	}
	result := claudeOAuthCredentials{AccessToken: credentials.AccessToken, SubscriptionType: credentials.SubscriptionType}
	if credentials.ClaudeAI != nil && credentials.ClaudeAI.AccessToken != "" {
		result.AccessToken = credentials.ClaudeAI.AccessToken
		result.SubscriptionType = credentials.ClaudeAI.SubscriptionType
	}
	if result.AccessToken == "" {
		return claudeOAuthCredentials{}, errors.New("Claude native OAuth token not found; run `claude auth login`")
	}
	return result, nil
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
