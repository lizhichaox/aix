package internal

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"
)

// DeepSeekSetupScriptURL is the official one-click installer that embeds the
// canonical Codex models.json payload.
const DeepSeekSetupScriptURL = "https://cdn.deepseek.com/api-docs/codex-deepseek-setup.sh"

// CatalogDiff describes one field-level difference between an AIX-generated
// catalog entry and an official catalog entry.
type CatalogDiff struct {
	Model    string
	Field    string
	AIX      string
	Official string
}

func (d CatalogDiff) String() string {
	return fmt.Sprintf("%s: %s: AIX=%s official=%s", d.Model, d.Field, d.AIX, d.Official)
}

// BundledDeepSeekCatalog returns AIX-owned fallback metadata for the built-in
// DeepSeek models. Official metadata is fetched at runtime when available;
// keeping the fallback generated avoids redistributing the upstream catalog.
func BundledDeepSeekCatalog() (map[string]map[string]interface{}, error) {
	models := []string{DeepSeekV4FlashModel, DeepSeekV4ProModel, DeepSeekV4VisionModel}
	catalog := make(map[string]map[string]interface{}, len(models))
	for _, model := range models {
		raw, err := json.Marshal(deepSeekV4Metadata(model, codexBaseInstructions))
		if err != nil {
			return nil, fmt.Errorf("encode bundled metadata for %s: %w", model, err)
		}
		var entry map[string]interface{}
		if err := json.Unmarshal(raw, &entry); err != nil {
			return nil, fmt.Errorf("normalize bundled metadata for %s: %w", model, err)
		}
		catalog[model] = entry
	}
	return catalog, nil
}

func parseOfficialCatalog(raw []byte) (map[string]map[string]interface{}, error) {
	var doc struct {
		Models []map[string]interface{} `json:"models"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse official DeepSeek catalog: %w", err)
	}
	out := make(map[string]map[string]interface{}, len(doc.Models))
	for _, m := range doc.Models {
		slug, _ := m["slug"].(string)
		if slug == "" {
			return nil, fmt.Errorf("official DeepSeek catalog contains a model without a slug")
		}
		out[slug] = m
	}
	return out, nil
}

var codexCatalogHeredocStart = regexp.MustCompile(`<<['"]?CODEX_MODELS_JSON['"]?`)

// ExtractDeepSeekCatalogFromSetupScript pulls the models.json payload out of
// the official setup script's quoted heredoc. It is kept separate from the
// fetch so the parser is testable without network access.
func ExtractDeepSeekCatalogFromSetupScript(script []byte) (map[string]map[string]interface{}, error) {
	const marker = "CODEX_MODELS_JSON"
	text := string(script)
	loc := codexCatalogHeredocStart.FindStringIndex(text)
	if loc == nil {
		return nil, fmt.Errorf("setup script: heredoc marker <<%s not found", marker)
	}
	start := loc[1]
	if nl := strings.IndexByte(text[start:], '\n'); nl >= 0 {
		start += nl + 1
	} else {
		return nil, fmt.Errorf("setup script: malformed %s heredoc", marker)
	}
	term := "\n" + marker + "\n"
	end := strings.Index(text[start:], term)
	if end < 0 {
		return nil, fmt.Errorf("setup script: heredoc terminator %s not found", marker)
	}
	return parseOfficialCatalog([]byte(text[start : start+end]))
}

// FetchOfficialDeepSeekCatalog downloads the official setup script and
// extracts the models.json payload it embeds.
func FetchOfficialDeepSeekCatalog() (map[string]map[string]interface{}, error) {
	return fetchOfficialDeepSeekCatalog(15 * time.Second)
}

// FetchOfficialDeepSeekCatalogQuick is the short-timeout variant used by
// best-effort runtime refreshes, where a slow catalog fetch must not delay a
// provider switch.
func FetchOfficialDeepSeekCatalogQuick() (map[string]map[string]interface{}, error) {
	return fetchOfficialDeepSeekCatalog(3 * time.Second)
}

func fetchOfficialDeepSeekCatalog(timeout time.Duration) (map[string]map[string]interface{}, error) {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(DeepSeekSetupScriptURL)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", DeepSeekSetupScriptURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: HTTP %d", DeepSeekSetupScriptURL, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", DeepSeekSetupScriptURL, err)
	}
	catalog, err := ExtractDeepSeekCatalogFromSetupScript(body)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", DeepSeekSetupScriptURL, err)
	}
	return catalog, nil
}

// catalogEntryFromSpec renders one model's metadata as a generic map, the
// same shape an official catalog entry takes after JSON decoding.
func catalogEntryFromSpec(spec NativeProviderSpec, model string) (map[string]interface{}, error) {
	metadata := minimalNativeCatalogMetadata(model)
	if spec.CatalogMetadata != nil {
		metadata = spec.CatalogMetadata(model)
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return nil, err
	}
	var entry map[string]interface{}
	if err := json.Unmarshal(raw, &entry); err != nil {
		return nil, err
	}
	return entry, nil
}

// catalogDiffs compares two catalogs keyed by model slug and returns
// field-level differences plus model presence differences.
func catalogDiffs(aix, official map[string]map[string]interface{}) []CatalogDiff {
	var diffs []CatalogDiff
	for slug := range aix {
		if _, ok := official[slug]; !ok {
			diffs = append(diffs, CatalogDiff{Model: slug, Field: "model", AIX: "present", Official: "absent"})
		}
	}
	for slug, offEntry := range official {
		atsEntry, ok := aix[slug]
		if !ok {
			diffs = append(diffs, CatalogDiff{Model: slug, Field: "model", AIX: "absent", Official: "present"})
			continue
		}
		for field, offVal := range offEntry {
			atsVal, ok := atsEntry[field]
			if !ok {
				diffs = append(diffs, CatalogDiff{Model: slug, Field: field, AIX: "<missing>", Official: formatCatalogValue(offVal)})
				continue
			}
			if !reflect.DeepEqual(atsVal, offVal) {
				diffs = append(diffs, CatalogDiff{Model: slug, Field: field, AIX: formatCatalogValue(atsVal), Official: formatCatalogValue(offVal)})
			}
		}
		for field, atsVal := range atsEntry {
			if _, ok := offEntry[field]; !ok {
				diffs = append(diffs, CatalogDiff{Model: slug, Field: field, AIX: formatCatalogValue(atsVal), Official: "<missing>"})
			}
		}
	}
	sort.Slice(diffs, func(i, j int) bool {
		if diffs[i].Model != diffs[j].Model {
			return diffs[i].Model < diffs[j].Model
		}
		return diffs[i].Field < diffs[j].Field
	})
	return diffs
}

// CatalogDiffs compares the metadata AIX generates for a native provider
// against a reference catalog, normally one freshly fetched from upstream.
func CatalogDiffs(providerID string, official map[string]map[string]interface{}) ([]CatalogDiff, error) {
	spec, ok := NativeProvider(providerID)
	if !ok {
		return nil, fmt.Errorf("unsupported Codex native provider %q", providerID)
	}
	aix := make(map[string]map[string]interface{}, len(spec.Models))
	for _, m := range spec.Models {
		entry, err := catalogEntryFromSpec(spec, m.Slug)
		if err != nil {
			return nil, fmt.Errorf("render %s catalog entry: %w", m.Slug, err)
		}
		aix[m.Slug] = entry
	}
	return catalogDiffs(aix, official), nil
}

// SnapshotCatalogDiffs compares two parsed catalogs.
func SnapshotCatalogDiffs(snapshot, remote map[string]map[string]interface{}) []CatalogDiff {
	return catalogDiffs(snapshot, remote)
}

func formatCatalogValue(v interface{}) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(raw)
}
