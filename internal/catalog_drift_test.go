package internal

import (
	"os"
	"reflect"
	"testing"
)

func TestBundledDeepSeekCatalogMatchesGeneratedMetadata(t *testing.T) {
	bundled, err := BundledDeepSeekCatalog()
	if err != nil {
		t.Fatalf("BundledDeepSeekCatalog: %v", err)
	}
	if len(bundled) != 3 {
		t.Fatalf("bundled catalog has %d models, want 3", len(bundled))
	}
	spec, ok := NativeProvider("deepseek")
	if !ok {
		t.Fatal("deepseek should be a registered native provider")
	}
	base, err := os.ReadFile("assets/codex_base_instructions.txt")
	if err != nil {
		t.Fatalf("read base instructions asset: %v", err)
	}
	for _, m := range spec.Models {
		entry, ok := bundled[m.Slug]
		if !ok {
			t.Errorf("bundled catalog missing model %q", m.Slug)
			continue
		}
		generated, err := catalogEntryFromSpec(spec, m.Slug)
		if err != nil {
			t.Fatalf("catalogEntryFromSpec(%s): %v", m.Slug, err)
		}
		if !reflect.DeepEqual(generated, entry) {
			diffs := catalogDiffs(
				map[string]map[string]interface{}{m.Slug: generated},
				map[string]map[string]interface{}{m.Slug: entry},
			)
			t.Errorf("AIX %s metadata differs from bundled catalog: %v", m.Slug, diffs)
		}
		messages, _ := entry["model_messages"].(map[string]interface{})
		if tmpl, _ := messages["instructions_template"].(string); tmpl != string(base) {
			t.Errorf("%s instructions_template differs from assets/codex_base_instructions.txt", m.Slug)
		}
		if instructions, _ := entry["base_instructions"].(string); instructions != string(base) {
			t.Errorf("%s base_instructions differs from assets/codex_base_instructions.txt", m.Slug)
		}
	}
}

func TestExtractDeepSeekCatalogFromSetupScript(t *testing.T) {
	script := `#!/bin/sh
cat > "$TMP" <<'CODEX_MODELS_JSON'
{"models":[{"slug":"deepseek-v4-flash","context_window":1048576}]}
CODEX_MODELS_JSON
echo done
`
	catalog, err := ExtractDeepSeekCatalogFromSetupScript([]byte(script))
	if err != nil {
		t.Fatalf("ExtractDeepSeekCatalogFromSetupScript: %v", err)
	}
	entry, ok := catalog["deepseek-v4-flash"]
	if !ok {
		t.Fatalf("extracted catalog missing deepseek-v4-flash: %v", catalog)
	}
	if entry["context_window"] != float64(1048576) {
		t.Errorf("context_window = %v, want 1048576", entry["context_window"])
	}
}

func TestExtractDeepSeekCatalogRejectsMissingHeredoc(t *testing.T) {
	if _, err := ExtractDeepSeekCatalogFromSetupScript([]byte("echo hi\n")); err == nil {
		t.Fatal("expected an error for a script without the models.json heredoc")
	}
}

func TestCatalogDiffs(t *testing.T) {
	official, err := BundledDeepSeekCatalog()
	if err != nil {
		t.Fatal(err)
	}
	diffs, err := CatalogDiffs("deepseek", official)
	if err != nil {
		t.Fatalf("CatalogDiffs: %v", err)
	}
	if len(diffs) != 0 {
		t.Fatalf("expected no drift, got: %v", diffs)
	}

	official["deepseek-v4-flash"]["context_window"] = float64(123)
	official["deepseek-v4-flash"]["new_field"] = "surprise"
	delete(official["deepseek-v4-pro"], "slug")
	diffs, err = CatalogDiffs("deepseek", official)
	if err != nil {
		t.Fatalf("CatalogDiffs: %v", err)
	}
	want := map[[2]string]bool{
		{"deepseek-v4-flash", "context_window"}: false,
		{"deepseek-v4-flash", "new_field"}:      false,
		{"deepseek-v4-pro", "slug"}:             false,
	}
	for _, d := range diffs {
		want[[2]string{d.Model, d.Field}] = true
	}
	for key, found := range want {
		if !found {
			t.Errorf("expected a diff for %s:%s", key[0], key[1])
		}
	}
}
