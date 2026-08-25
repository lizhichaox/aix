package internal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveLoadRemoveUserNativeProvider(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	spec := UserNativeProvider{
		ID:           "mynative",
		Name:         "My Native",
		EnvKey:       "MYNATIVE_API_KEY",
		BaseURL:      "https://api.mynative.com/v1",
		DefaultModel: "my-model",
		Models:       []string{"my-model", "my-model-2"},
	}
	if err := SaveUserNativeProvider(spec); err != nil {
		t.Fatalf("SaveUserNativeProvider: %v", err)
	}
	users, err := LoadUserNativeProviders()
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0].ID != "mynative" {
		t.Fatalf("loaded users = %+v", users)
	}

	if err := RemoveUserNativeProvider("mynative"); err != nil {
		t.Fatalf("RemoveUserNativeProvider: %v", err)
	}
	users, err = LoadUserNativeProviders()
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 0 {
		t.Errorf("expected no users after remove, got %+v", users)
	}
}

func TestNativeProviderSpecsIncludesUserProviders(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	spec := UserNativeProvider{
		ID:           "mynative",
		Name:         "My Native",
		EnvKey:       "MYNATIVE_API_KEY",
		BaseURL:      "https://api.mynative.com/v1",
		DefaultModel: "my-model",
		Models:       []string{"my-model", "my-model-2"},
	}
	if err := SaveUserNativeProvider(spec); err != nil {
		t.Fatal(err)
	}
	got, ok := NativeProvider("mynative")
	if !ok {
		t.Fatal("user native provider should resolve in registry")
	}
	if got.BaseURL != spec.BaseURL || got.EnvKey != spec.EnvKey || got.DefaultModel != "my-model" {
		t.Errorf("resolved spec = %+v", got)
	}
	if models := NativeModels("mynative"); len(models) != 2 || models[0] != "my-model" {
		t.Errorf("NativeModels(mynative) = %v", models)
	}
	if _, err := ResolveNativeModel("mynative", ""); err != nil {
		t.Errorf("default model should resolve: %v", err)
	}
	if _, err := ResolveNativeModel("mynative", "bogus"); err == nil {
		t.Error("invalid user native model should be rejected")
	}
}

func TestEnsureProviderTemplateUserNative(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	spec := UserNativeProvider{
		ID:           "mynative",
		Name:         "My Native",
		EnvKey:       "MYNATIVE_API_KEY",
		BaseURL:      "https://api.mynative.com/v1",
		DefaultModel: "my-model",
		Models:       []string{"my-model"},
	}
	if err := SaveUserNativeProvider(spec); err != nil {
		t.Fatal(err)
	}
	created, err := EnsureProviderTemplate("codex", "mynative")
	if err != nil || !created {
		t.Fatalf("EnsureProviderTemplate = (%v, %v), want created", created, err)
	}
	path := filepath.Join(AixDir(), "apps", "codex", "mynative.toml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(raw)
	if !strings.Contains(content, "mode = \"proxy\"") || !strings.Contains(content, "model = \"my-model\"") {
		t.Errorf("unexpected managed Responses template: %s", content)
	}
	created, err = EnsureProviderTemplate("codex", "mynative")
	if err != nil || created {
		t.Fatalf("second EnsureProviderTemplate = (%v, %v), want not created", created, err)
	}
}

func TestUserNativeModelLifecycle(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	spec := UserNativeProvider{
		ID:           "mynative",
		Name:         "My Native",
		EnvKey:       "MYNATIVE_API_KEY",
		BaseURL:      "https://api.mynative.com/v1",
		DefaultModel: "my-model",
		Models:       []string{"my-model"},
	}
	if err := SaveUserNativeProvider(spec); err != nil {
		t.Fatal(err)
	}
	if err := AddUserNativeModel("mynative", "my-model-2"); err != nil {
		t.Fatalf("AddUserNativeModel: %v", err)
	}
	if err := AddUserNativeModel("mynative", "my-model-2"); err == nil {
		t.Error("duplicate model should be rejected")
	}
	if err := SetUserNativeModel("mynative", "my-model-2"); err != nil {
		t.Errorf("SetUserNativeModel on existing model should be idempotent: %v", err)
	}
	if err := SetUserNativeModel("mynative", "my-model-3"); err != nil {
		t.Errorf("SetUserNativeModel new model: %v", err)
	}
	if err := RemoveUserNativeModel("mynative", "my-model"); err != nil {
		t.Fatalf("RemoveUserNativeModel: %v", err)
	}
	users, err := LoadUserNativeProviders()
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0].DefaultModel != "my-model-2" {
		t.Errorf("default should fall back to first remaining model: %+v", users)
	}
	if len(users[0].Models) != 2 {
		t.Errorf("models = %v, want 2", users[0].Models)
	}
	if err := RemoveUserNativeModel("mynative", "nope"); err == nil {
		t.Error("removing a missing model should fail")
	}
}
