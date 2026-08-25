package internal

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	HarnessCodex  = "codex"
	HarnessClaude = "claude"

	APIFormatResponses = "responses"
	APIFormatAnthropic = "anthropic"
)

var (
	codexEffortTokens = []string{"minimal", "low", "medium", "high", "xhigh", "max", "ultra"}
	codexEfforts      = []string{"low", "high", "max"}
	claudeEfforts     = []string{"low", "medium", "high", "xhigh", "max"}
)

// HarnessModelSpec describes one model as exposed to a particular harness.
// ClientModel is written to the harness configuration; UpstreamModel is what
// the provider receives after any harness-specific alias rewrite.
type HarnessModelSpec struct {
	ID               string   `toml:"-"`
	ClientModel      string   `toml:"client_model"`
	UpstreamModel    string   `toml:"upstream_model"`
	DisplayName      string   `toml:"display_name"`
	ContextWindow    int      `toml:"context_window,omitempty"`
	DefaultEffort    string   `toml:"default_effort"`
	SupportedEfforts []string `toml:"supported_efforts"`
}

// HarnessProviderSpec is the provider configuration visible to one harness.
// Model catalogs intentionally live here rather than on the shared provider:
// protocol compatibility differs between Responses and Anthropic Messages.
type HarnessProviderSpec struct {
	HarnessID     string                      `toml:"-"`
	ProviderID    string                      `toml:"-"`
	APIFormat     string                      `toml:"api_format"`
	BaseURL       string                      `toml:"base_url"`
	DefaultModel  string                      `toml:"default_model"`
	DefaultEffort string                      `toml:"default_effort"`
	Models        map[string]HarnessModelSpec `toml:"models"`
}

type HarnessProviderMappings struct {
	Harnesses map[string]HarnessProviderSpec `toml:"harnesses"`
}

// HarnessRegistryConfig is the editable on-disk mapping format. Provider
// metadata is grouped first, then each harness owns its protocol-specific
// model catalog and defaults.
type HarnessRegistryConfig struct {
	Version   int                                `toml:"version"`
	Providers map[string]HarnessProviderMappings `toml:"providers"`
}

// HarnessSelection is the fully resolved command input consumed by a harness
// adapter. Model and effort defaults have already been applied and validated.
type HarnessSelection struct {
	HarnessID     string
	ProviderID    string
	Model         string
	ClientModel   string
	UpstreamModel string
	ContextWindow int
	Effort        string
}

// HarnessProvider returns the effective built-in mapping for one
// harness/provider pair. Codex and Claude catalogs remain strictly separate.
func bundledHarnessProvider(harnessID, providerID string) (HarnessProviderSpec, bool) {
	switch harnessID {
	case HarnessCodex:
		native, ok := NativeProvider(providerID)
		if !ok {
			return HarnessProviderSpec{}, false
		}
		models := make(map[string]HarnessModelSpec, len(native.Models))
		for _, m := range native.Models {
			models[m.Slug] = HarnessModelSpec{
				ID:               m.Slug,
				ClientModel:      m.Slug,
				UpstreamModel:    m.Slug,
				DisplayName:      m.DisplayName,
				ContextWindow:    m.ContextWindow,
				DefaultEffort:    DefaultHarnessEffort,
				SupportedEfforts: append([]string(nil), codexEfforts...),
			}
		}
		return HarnessProviderSpec{
			HarnessID:     harnessID,
			ProviderID:    providerID,
			APIFormat:     APIFormatResponses,
			BaseURL:       native.BaseURL,
			DefaultModel:  native.DefaultModel,
			DefaultEffort: DefaultHarnessEffort,
			Models:        models,
		}, true
	case HarnessClaude:
		claude, ok := ClaudeProviderSpecFor(providerID)
		if !ok {
			return HarnessProviderSpec{}, false
		}
		preset, ok := KnownProviders()[providerID]
		if !ok || !preset.AnthropicNative {
			return HarnessProviderSpec{}, false
		}
		models := make(map[string]HarnessModelSpec, len(claude.Models))
		for _, m := range claude.Models {
			models[m.Upstream] = HarnessModelSpec{
				ID:               m.Upstream,
				ClientModel:      m.Alias,
				UpstreamModel:    m.Upstream,
				DisplayName:      m.DisplayName,
				ContextWindow:    m.ContextWindow,
				DefaultEffort:    claude.DefaultEffort,
				SupportedEfforts: append([]string(nil), claudeEfforts...),
			}
		}
		return HarnessProviderSpec{
			HarnessID:     harnessID,
			ProviderID:    providerID,
			APIFormat:     APIFormatAnthropic,
			BaseURL:       preset.AnthropicUpstream,
			DefaultModel:  claude.DefaultModel,
			DefaultEffort: claude.DefaultEffort,
			Models:        models,
		}, true
	default:
		return HarnessProviderSpec{}, false
	}
}

// BundledHarnessRegistry returns the mapping shipped with AIX. It is used when
// no user file exists and as the initial content for `--edit`.
func BundledHarnessRegistry() HarnessRegistryConfig {
	providerIDs := make(map[string]bool)
	for id := range NativeProviderSpecs() {
		providerIDs[id] = true
	}
	for id := range ClaudeProviderSpecs() {
		providerIDs[id] = true
	}
	ids := make([]string, 0, len(providerIDs))
	for id := range providerIDs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	registry := HarnessRegistryConfig{Version: 2, Providers: make(map[string]HarnessProviderMappings)}
	for _, providerID := range ids {
		harnesses := make(map[string]HarnessProviderSpec)
		for _, harnessID := range []string{HarnessCodex, HarnessClaude} {
			if spec, ok := bundledHarnessProvider(harnessID, providerID); ok {
				harnesses[harnessID] = spec
			}
		}
		if len(harnesses) > 0 {
			registry.Providers[providerID] = HarnessProviderMappings{Harnesses: harnesses}
		}
	}
	return registry
}

// LoadHarnessRegistry loads the user mapping when present; otherwise it
// returns the bundled registry. A user file is authoritative so removing a
// harness or model from it removes that mapping from the effective registry.
func LoadHarnessRegistry() (HarnessRegistryConfig, error) {
	path := HarnessRegistryPath()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return BundledHarnessRegistry(), nil
	} else if err != nil {
		return HarnessRegistryConfig{}, fmt.Errorf("stat %s: %w", path, err)
	}
	var registry HarnessRegistryConfig
	if _, err := toml.DecodeFile(path, &registry); err != nil {
		return HarnessRegistryConfig{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if registry.Version != 1 && registry.Version != 2 {
		return HarnessRegistryConfig{}, fmt.Errorf("unsupported harness registry version %d in %s (expected 1 or 2)", registry.Version, path)
	}
	if registry.Providers == nil {
		registry.Providers = map[string]HarnessProviderMappings{}
	}
	bundled := BundledHarnessRegistry()
	if registry.Version == 1 {
		// Version 2 adds the two documented-but-previously-unlisted DeepSeek
		// Messages mappings for OpenCode Go. Merge only those additions so all
		// other user catalog edits remain authoritative.
		if provider, exists := registry.Providers["opencode-go"]; exists {
			if claude, exists := provider.Harnesses[HarnessClaude]; exists {
				bundledClaude := bundled.Providers["opencode-go"].Harnesses[HarnessClaude]
				if claude.Models == nil {
					claude.Models = map[string]HarnessModelSpec{}
				}
				for _, modelID := range []string{DeepSeekV4FlashModel, DeepSeekV4ProModel} {
					if _, exists := claude.Models[modelID]; !exists {
						claude.Models[modelID] = bundledClaude.Models[modelID]
					}
				}
				provider.Harnesses[HarnessClaude] = claude
				registry.Providers["opencode-go"] = provider
			}
		}
		registry.Version = 2
	}
	for providerID, provider := range registry.Providers {
		if provider.Harnesses == nil {
			provider.Harnesses = map[string]HarnessProviderSpec{}
		}
		for harnessID, spec := range provider.Harnesses {
			spec.ProviderID = providerID
			spec.HarnessID = harnessID
			if spec.Models == nil {
				spec.Models = map[string]HarnessModelSpec{}
			}
			for modelID, model := range spec.Models {
				model.ID = modelID
				if model.ClientModel == "" {
					model.ClientModel = modelID
				}
				if model.UpstreamModel == "" {
					model.UpstreamModel = modelID
				}
				if model.DefaultEffort == "" {
					model.DefaultEffort = spec.DefaultEffort
				}
				// Registries materialized before context_window was introduced do
				// not carry capability metadata. Treat zero as unspecified and
				// inherit the bundled value for the same harness/provider/model;
				// all routing names and user defaults remain authoritative.
				if model.ContextWindow == 0 {
					if bundledProvider, ok := bundled.Providers[providerID]; ok {
						if bundledHarness, ok := bundledProvider.Harnesses[harnessID]; ok {
							if bundledModel, ok := bundledHarness.Models[modelID]; ok {
								model.ContextWindow = bundledModel.ContextWindow
							}
						}
					}
				}
				spec.Models[modelID] = model
			}
			provider.Harnesses[harnessID] = spec
		}
		registry.Providers[providerID] = provider
	}
	return registry, nil
}

// WriteHarnessRegistry writes a complete editable registry with private file
// permissions because provider configuration may reveal internal endpoints.
func WriteHarnessRegistry(path string, registry HarnessRegistryConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	var content bytes.Buffer
	content.WriteString("# AIX harness provider/model/effort mappings.\n")
	content.WriteString("# Each harness has its own API format and model catalog. Map keys are logical model IDs.\n")
	content.WriteString("# default_model must name a model key; default_effort must be supported by that model.\n\n")
	if err := toml.NewEncoder(&content).Encode(registry); err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".harnesses-*.toml")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(content.Bytes()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// EnsureHarnessRegistryFile materializes the bundled defaults for editing.
func EnsureHarnessRegistryFile() (string, error) {
	path := HarnessRegistryPath()
	if _, err := os.Stat(path); err == nil {
		return path, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := WriteHarnessRegistry(path, BundledHarnessRegistry()); err != nil {
		return "", err
	}
	return path, nil
}

func harnessProviderWithError(harnessID, providerID string) (HarnessProviderSpec, bool, error) {
	registry, err := LoadHarnessRegistry()
	if err != nil {
		return HarnessProviderSpec{}, false, err
	}
	provider, ok := registry.Providers[providerID]
	if !ok {
		return HarnessProviderSpec{}, false, nil
	}
	spec, ok := provider.Harnesses[harnessID]
	return spec, ok, nil
}

// HarnessProvider returns an effective mapping. Callers that need diagnostic
// parse errors should use ResolveHarnessSelection or LoadHarnessRegistry.
func HarnessProvider(harnessID, providerID string) (HarnessProviderSpec, bool) {
	spec, ok, err := harnessProviderWithError(harnessID, providerID)
	return spec, ok && err == nil
}

// ResolveHarnessSelection applies harness-specific model normalization, then
// applies and validates the effort default from the same harness mapping.
func ResolveHarnessSelection(harnessID, providerID, model, effort string) (HarnessSelection, error) {
	spec, ok, loadErr := harnessProviderWithError(harnessID, providerID)
	if loadErr != nil {
		return HarnessSelection{}, loadErr
	}
	if !ok {
		return HarnessSelection{}, fmt.Errorf("provider %q is not configured for harness %q", providerID, harnessID)
	}
	wantAPI := APIFormatResponses
	if harnessID == HarnessClaude {
		wantAPI = APIFormatAnthropic
	}
	if spec.APIFormat != wantAPI {
		return HarnessSelection{}, fmt.Errorf("invalid %s/%s mapping: api_format must be %q, got %q (run --doctor)", harnessID, providerID, wantAPI, spec.APIFormat)
	}
	parsedBaseURL, baseErr := url.Parse(spec.BaseURL)
	if baseErr != nil || parsedBaseURL.Host == "" || (parsedBaseURL.Scheme != "http" && parsedBaseURL.Scheme != "https") {
		return HarnessSelection{}, fmt.Errorf("invalid %s/%s base_url %q (run --doctor)", harnessID, providerID, spec.BaseURL)
	}

	modelWasDefaulted := strings.TrimSpace(model) == ""
	var resolved HarnessModelSpec
	switch harnessID {
	case HarnessCodex:
		requested := strings.TrimSpace(model)
		if requested == "" {
			requested = spec.DefaultModel
		}
		found := false
		for id, candidate := range spec.Models {
			if strings.EqualFold(requested, id) || strings.EqualFold(requested, candidate.ClientModel) || strings.EqualFold(requested, candidate.UpstreamModel) || strings.EqualFold(requested, candidate.DisplayName) {
				resolved = candidate
				resolved.ID = id
				found = true
				break
			}
		}
		if !found {
			modelID, err := ResolveNativeModel(providerID, requested)
			if err != nil {
				return HarnessSelection{}, err
			}
			requested = modelID
			resolved, found = spec.Models[modelID]
			if found {
				resolved.ID = modelID
			}
		}
		if !found {
			// AllowAnyModel providers accept explicit models outside the bundled
			// catalog. They remain scoped to this harness and provider.
			resolved = HarnessModelSpec{
				ID:               requested,
				ClientModel:      requested,
				UpstreamModel:    requested,
				DisplayName:      requested,
				SupportedEfforts: append([]string(nil), codexEfforts...),
				DefaultEffort:    spec.DefaultEffort,
			}
		}
	case HarnessClaude:
		requested := strings.TrimSuffix(strings.TrimSpace(model), "[1m]")
		if requested == "" {
			requested = spec.DefaultModel
		}
		found := false
		for id, candidate := range spec.Models {
			if requested == id || requested == candidate.ClientModel || requested == candidate.UpstreamModel {
				resolved = candidate
				resolved.ID = id
				found = true
				break
			}
		}
		if !found && providerID == "deepseek" && ValidDeepSeekUpstreamModel(requested) {
			alias, displayName := ClaudeDeepSeekAlias(requested)
			resolved = HarnessModelSpec{ID: requested, ClientModel: alias, UpstreamModel: requested, DisplayName: displayName, ContextWindow: deepSeekV4ContextWindow, DefaultEffort: spec.DefaultEffort, SupportedEfforts: append([]string(nil), claudeEfforts...)}
			found = true
		}
		if !found && providerID == "openrouter" && strings.Contains(requested, "/") {
			resolved = HarnessModelSpec{ID: requested, ClientModel: requested, UpstreamModel: requested, DisplayName: requested, DefaultEffort: spec.DefaultEffort, SupportedEfforts: append([]string(nil), claudeEfforts...)}
			found = true
		}
		if !found {
			return HarnessSelection{}, fmt.Errorf("unsupported %s/%s model %q (use %s)", harnessID, providerID, requested, strings.Join(sortedHarnessModelIDs(spec.Models), ", "))
		}
	}

	if strings.TrimSpace(effort) == "" {
		effort = resolved.DefaultEffort
		if modelWasDefaulted || effort == "" {
			effort = spec.DefaultEffort
		}
	}
	effort = strings.ToLower(strings.TrimSpace(effort))
	if !containsString(resolved.SupportedEfforts, effort) {
		return HarnessSelection{}, fmt.Errorf("unsupported effort %q for %s/%s model %q (use %s)", effort, harnessID, providerID, resolved.ID, strings.Join(resolved.SupportedEfforts, ", "))
	}
	return HarnessSelection{
		HarnessID:     harnessID,
		ProviderID:    providerID,
		Model:         resolved.ID,
		ClientModel:   resolved.ClientModel,
		UpstreamModel: resolved.UpstreamModel,
		ContextWindow: resolved.ContextWindow,
		Effort:        effort,
	}, nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// IsHarnessEffort reports whether value is a recognized effort token for the
// named harness. It is used by CLI parsers to disambiguate the final optional
// positional argument from multi-word model display names.
func IsHarnessEffort(harnessID, value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	switch harnessID {
	case HarnessCodex:
		return containsString(codexEffortTokens, value)
	case HarnessClaude:
		return containsString(claudeEfforts, value)
	default:
		return false
	}
}

// HarnessEfforts returns the effort tokens accepted by a harness mapping.
func HarnessEfforts(harnessID string) []string {
	switch harnessID {
	case HarnessCodex:
		return append([]string(nil), codexEffortTokens...)
	case HarnessClaude:
		return append([]string(nil), claudeEfforts...)
	default:
		return nil
	}
}
