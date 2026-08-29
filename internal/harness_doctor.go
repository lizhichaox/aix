package internal

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

type HarnessDiagnostic struct {
	Severity string
	Path     string
	Reason   string
	Suggest  string
}

// DiagnoseHarnessRegistry validates the effective mapping without changing it.
// Empty providerID checks every provider configured for the harness.
func DiagnoseHarnessRegistry(harnessID, providerID string) []HarnessDiagnostic {
	registry, err := LoadHarnessRegistry()
	if err != nil {
		return []HarnessDiagnostic{{
			Severity: "error",
			Path:     HarnessRegistryPath(harnessID),
			Reason:   err.Error(),
			Suggest:  "fix the TOML syntax or move the file aside and run --edit to regenerate defaults",
		}}
	}
	var diagnostics []HarnessDiagnostic
	providerIDs := make([]string, 0, len(registry.Providers))
	for id := range registry.Providers {
		if providerID == "" || id == providerID {
			providerIDs = append(providerIDs, id)
		}
	}
	sort.Strings(providerIDs)
	if providerID != "" && len(providerIDs) == 0 {
		return []HarnessDiagnostic{{Severity: "error", Path: harnessID + "/" + providerID, Reason: "provider is absent from the harness registry", Suggest: "run --edit and add the provider mapping or choose a listed provider"}}
	}
	for _, id := range providerIDs {
		provider := registry.Providers[id]
		spec, ok := provider.Harnesses[harnessID]
		path := harnessID + "/" + id
		if !ok {
			if providerID != "" {
				diagnostics = append(diagnostics, HarnessDiagnostic{Severity: "error", Path: path, Reason: "provider has no mapping for this harness", Suggest: "add providers." + id + ".harnesses." + harnessID + " with the correct API format and models"})
			}
			continue
		}
		wantAPI := APIFormatResponses
		if harnessID == HarnessClaude {
			wantAPI = APIFormatAnthropic
		}
		if spec.APIFormat != wantAPI {
			diagnostics = append(diagnostics, HarnessDiagnostic{Severity: "error", Path: path + ".api_format", Reason: fmt.Sprintf("%s harness requires %q, got %q", harnessID, wantAPI, spec.APIFormat), Suggest: "set api_format = " + fmt.Sprintf("%q", wantAPI)})
		}
		if parsed, parseErr := url.Parse(spec.BaseURL); parseErr != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			diagnostics = append(diagnostics, HarnessDiagnostic{Severity: "error", Path: path + ".base_url", Reason: fmt.Sprintf("invalid HTTP(S) base URL %q", spec.BaseURL), Suggest: "set the provider's protocol-specific API base URL"})
		}
		if len(spec.Models) == 0 {
			diagnostics = append(diagnostics, HarnessDiagnostic{Severity: "error", Path: path + ".models", Reason: "model mapping is empty", Suggest: "add at least one model with client_model, upstream_model, and supported_efforts"})
			continue
		}
		defaultModel, defaultOK := spec.Models[spec.DefaultModel]
		if spec.DefaultModel == "" || !defaultOK {
			diagnostics = append(diagnostics, HarnessDiagnostic{Severity: "error", Path: path + ".default_model", Reason: fmt.Sprintf("default model %q is not a model-map key", spec.DefaultModel), Suggest: "set default_model to one of: " + strings.Join(sortedHarnessModelIDs(spec.Models), ", ")})
		}
		if !IsHarnessEffort(harnessID, spec.DefaultEffort) {
			diagnostics = append(diagnostics, HarnessDiagnostic{Severity: "error", Path: path + ".default_effort", Reason: fmt.Sprintf("effort %q is not recognized by %s", spec.DefaultEffort, harnessID), Suggest: "use one of: " + strings.Join(HarnessEfforts(harnessID), ", ")})
		} else if defaultOK && !containsString(defaultModel.SupportedEfforts, spec.DefaultEffort) {
			diagnostics = append(diagnostics, HarnessDiagnostic{Severity: "error", Path: path + ".default_effort", Reason: fmt.Sprintf("default model %q does not support effort %q", spec.DefaultModel, spec.DefaultEffort), Suggest: "choose one of: " + strings.Join(defaultModel.SupportedEfforts, ", ")})
		}

		clientModels := make(map[string]string)
		for modelID, model := range spec.Models {
			modelPath := path + ".models." + modelID
			if strings.TrimSpace(model.ClientModel) == "" {
				diagnostics = append(diagnostics, HarnessDiagnostic{Severity: "error", Path: modelPath + ".client_model", Reason: "client model is empty", Suggest: "set the model ID written to the harness"})
			}
			if strings.TrimSpace(model.UpstreamModel) == "" {
				diagnostics = append(diagnostics, HarnessDiagnostic{Severity: "error", Path: modelPath + ".upstream_model", Reason: "upstream model is empty", Suggest: "set the model ID accepted by this provider endpoint"})
			}
			if previous, exists := clientModels[model.ClientModel]; exists && previous != model.UpstreamModel {
				diagnostics = append(diagnostics, HarnessDiagnostic{Severity: "error", Path: modelPath + ".client_model", Reason: fmt.Sprintf("client model %q maps to both %q and %q", model.ClientModel, previous, model.UpstreamModel), Suggest: "use unique client aliases within one harness/provider mapping"})
			} else {
				clientModels[model.ClientModel] = model.UpstreamModel
			}
			if len(model.SupportedEfforts) == 0 {
				diagnostics = append(diagnostics, HarnessDiagnostic{Severity: "error", Path: modelPath + ".supported_efforts", Reason: "supported effort list is empty", Suggest: "declare the effort values accepted by this harness model"})
			}
			if !IsHarnessEffort(harnessID, model.DefaultEffort) {
				diagnostics = append(diagnostics, HarnessDiagnostic{Severity: "error", Path: modelPath + ".default_effort", Reason: fmt.Sprintf("model default effort %q is not recognized by %s", model.DefaultEffort, harnessID), Suggest: "use one of: " + strings.Join(HarnessEfforts(harnessID), ", ")})
			} else if !containsString(model.SupportedEfforts, model.DefaultEffort) {
				diagnostics = append(diagnostics, HarnessDiagnostic{Severity: "error", Path: modelPath + ".default_effort", Reason: fmt.Sprintf("model does not include default effort %q in supported_efforts", model.DefaultEffort), Suggest: "choose one of: " + strings.Join(model.SupportedEfforts, ", ")})
			}
			for _, effort := range model.SupportedEfforts {
				if !IsHarnessEffort(harnessID, effort) {
					diagnostics = append(diagnostics, HarnessDiagnostic{Severity: "error", Path: modelPath + ".supported_efforts", Reason: fmt.Sprintf("unsupported effort token %q", effort), Suggest: "use values from: " + strings.Join(HarnessEfforts(harnessID), ", ")})
				}
			}
			if model.ContextWindow < 0 {
				diagnostics = append(diagnostics, HarnessDiagnostic{Severity: "error", Path: modelPath + ".context_window", Reason: "context window cannot be negative", Suggest: "set zero when unknown or a positive token count"})
			}
		}

		if _, ok := bundledHarnessProvider(harnessID, id); !ok {
			diagnostics = append(diagnostics, HarnessDiagnostic{Severity: "error", Path: path, Reason: "no installed harness adapter knows how to authenticate or apply this provider", Suggest: "add the provider to AIX's provider registry before using this mapping"})
		} else if key, _ := NativeProviderAPIKey(id); harnessID == HarnessCodex && key == "" {
			native, _ := NativeProvider(id)
			diagnostics = append(diagnostics, HarnessDiagnostic{Severity: "warning", Path: path, Reason: "provider API key is not configured", Suggest: "set $" + native.EnvKey + " before switching"})
		} else if harnessID == HarnessClaude && claudeProviderEnvKey(id) == "" {
			preset := KnownProviders()[id]
			diagnostics = append(diagnostics, HarnessDiagnostic{Severity: "warning", Path: path, Reason: "provider API key is not configured", Suggest: "set $" + preset.EnvVar + " before switching"})
		}
	}
	return diagnostics
}

func sortedHarnessModelIDs(models map[string]HarnessModelSpec) []string {
	ids := make([]string, 0, len(models))
	for id := range models {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
