package internal

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// HarnessInfo describes a client shell that consumes provider/model mappings.
// The on-disk "apps" directory name is retained for backward compatibility.
type HarnessInfo struct {
	ID        string
	Name      string
	Dir       string
	ApplyFunc func(providerName string, data map[string]interface{}) error
}

// AppInfo is a compatibility alias for integrations built before the harness
// terminology was introduced.
type AppInfo = HarnessInfo

var harnesses = []*HarnessInfo{
	{ID: "claudecode", Name: "Claude Code", Dir: "claudecode"},
	{ID: "desktop", Name: "Claude Desktop", Dir: "desktop"},
	{ID: "codex", Name: "Codex", Dir: "codex"},
	{ID: "excalidraw", Name: "Excalidraw (Obsidian)", Dir: "excalidraw"},
}

var aliases = map[string]string{
	"claude-code":    "claudecode",
	"cc":             "claudecode",
	"claude-desktop": "desktop",
	"claude":         "desktop",
	"cd":             "desktop",
	"cx":             "codex",
	"ex":             "excalidraw",
}

func init() {
	for _, a := range harnesses {
		a.ApplyFunc = getApplyFunc(a.ID)
	}
}

func ResolveHarness(name string) (*HarnessInfo, error) {
	name = strings.ToLower(name)
	if mapped, ok := aliases[name]; ok {
		name = mapped
	}
	for _, a := range harnesses {
		if a.ID == name {
			return a, nil
		}
	}
	return nil, fmt.Errorf("unknown harness '%s' (use: claudecode, desktop, codex, excalidraw)", name)
}

func AllHarnesses() []*HarnessInfo {
	return harnesses
}

// ResolveApp and AllApps are compatibility wrappers.
func ResolveApp(name string) (*HarnessInfo, error) {
	return ResolveHarness(name)
}

func AllApps() []*HarnessInfo {
	return AllHarnesses()
}

func AppDir(appID string) string {
	return filepath.Join(AixDir(), "apps", appID)
}

func ListProviders(appID string) ([]string, error) {
	dir := AppDir(appID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			entries = nil
		} else {
			return nil, err
		}
	}
	names := make([]string, 0, len(entries)+4)
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".toml") {
			names = append(names, strings.TrimSuffix(e.Name(), ".toml"))
		}
	}
	// Claude clients only support Anthropic-native providers: always expose
	// the built-in ones and hide templates whose provider cannot speak the
	// Anthropic Messages API without conversion.
	if appID == "claudecode" || appID == "desktop" {
		seen := make(map[string]bool, len(names)+1)
		out := make([]string, 0, len(names)+1)
		registry, _ := LoadHarnessRegistry()
		for id, provider := range registry.Providers {
			if _, mapped := provider.Harnesses[HarnessClaude]; mapped && !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
		for _, n := range names {
			if seen[n] {
				continue
			}
			seen[n] = true
			if IsAnthropicNativeProvider(n) {
				out = append(out, n)
			}
		}
		sort.Strings(out)
		return out, nil
	}
	// Codex only supports native Responses API providers: always expose the
	// registry and hide legacy proxy/custom templates that would need
	// protocol conversion.
	if appID == "codex" {
		seen := make(map[string]bool, len(names))
		registry, _ := LoadHarnessRegistry()
		out := make([]string, 0, len(names)+len(registry.Providers))
		for id, provider := range registry.Providers {
			if _, mapped := provider.Harnesses[HarnessCodex]; mapped && IsNativeProvider(id) && !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
		for _, n := range names {
			if seen[n] {
				continue
			}
			seen[n] = true
			if IsNativeProvider(n) {
				out = append(out, n)
			}
		}
		sort.Strings(out)
		return out, nil
	}
	return names, nil
}

// ShortAlias returns the short alias for an app ID (cc/cd/cx) for CLI usage.
func ShortAlias(appID string) string {
	switch appID {
	case "claudecode":
		return "cc"
	case "desktop":
		return "cd"
	case "codex":
		return "cx"
	case "excalidraw":
		return "ex"
	default:
		return appID
	}
}

// AppAbbrev returns a short label for an app ID (CC/CD/CX/EX/OC).
func AppAbbrev(appID string) string {
	switch appID {
	case "claudecode":
		return "CC"
	case "desktop":
		return "CD"
	case "codex":
		return "CX"
	case "excalidraw":
		return "EX"
	case "opencode":
		return "OC"
	default:
		if appID == "" {
			return "-"
		}
		return appID
	}
}

// AppDisplayName returns human-readable name for an app ID.
func AppDisplayName(appID string) string {
	for _, a := range harnesses {
		if a.ID == appID {
			return a.Name
		}
	}
	if appID == "" {
		return "-"
	}
	return appID
}

func ProviderPath(appID, provider string) string {
	if strings.ContainsAny(provider, "/\\") || strings.Contains(provider, "..") {
		return ""
	}
	return filepath.Join(AppDir(appID), provider+".toml")
}
