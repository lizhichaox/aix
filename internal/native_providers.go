package internal

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// UserNativeProvider is a user-defined Codex native provider persisted in
// ~/.aix/native.toml.
type UserNativeProvider struct {
	ID           string   `toml:"id"`
	Name         string   `toml:"name"`
	EnvKey       string   `toml:"env_key"`
	BaseURL      string   `toml:"base_url"`
	DefaultModel string   `toml:"default_model"`
	Models       []string `toml:"models"`
}

type nativeProvidersFile struct {
	Providers []UserNativeProvider `toml:"providers"`
}

// NativeProvidersPath returns the user-defined native provider config path.
func NativeProvidersPath() string {
	return filepath.Join(AixDir(), "native.toml")
}

// LoadUserNativeProviders reads user-defined native providers from
// ~/.aix/native.toml.
func LoadUserNativeProviders() ([]UserNativeProvider, error) {
	var f nativeProvidersFile
	raw, err := os.ReadFile(NativeProvidersPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if _, err := toml.Decode(string(raw), &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", NativeProvidersPath(), err)
	}
	return f.Providers, nil
}

// SaveUserNativeProvider upserts a user-defined native provider.
func SaveUserNativeProvider(p UserNativeProvider) error {
	if p.ID == "deepseek" {
		return fmt.Errorf("provider ID %q is reserved", p.ID)
	}
	if strings.TrimSpace(p.ID) == "" || strings.TrimSpace(p.Name) == "" ||
		strings.TrimSpace(p.BaseURL) == "" || strings.TrimSpace(p.DefaultModel) == "" {
		return fmt.Errorf("native provider requires id, name, base_url, and default_model")
	}
	existing, err := LoadUserNativeProviders()
	if err != nil {
		return err
	}
	replaced := false
	for i, e := range existing {
		if e.ID == p.ID {
			existing[i] = p
			replaced = true
			break
		}
	}
	if !replaced {
		existing = append(existing, p)
	}
	return writeNativeProvidersFile(existing)
}

// RemoveUserNativeProvider deletes a user-defined native provider.
func RemoveUserNativeProvider(id string) error {
	existing, err := LoadUserNativeProviders()
	if err != nil {
		return err
	}
	filtered := existing[:0]
	for _, e := range existing {
		if e.ID != id {
			filtered = append(filtered, e)
		}
	}
	if len(filtered) == len(existing) {
		return fmt.Errorf("native provider %q not found in %s", id, NativeProvidersPath())
	}
	return writeNativeProvidersFile(filtered)
}

// AddUserNativeModel appends a model to a user native provider, failing when
// it already exists. The first added model becomes the default.
func AddUserNativeModel(id, model string) error {
	if strings.TrimSpace(model) == "" {
		return fmt.Errorf("model is required")
	}
	users, err := LoadUserNativeProviders()
	if err != nil {
		return err
	}
	for i := range users {
		if users[i].ID != id {
			continue
		}
		for _, m := range users[i].Models {
			if m == model {
				return fmt.Errorf("model %q already exists for native provider %q", model, id)
			}
		}
		users[i].Models = append(users[i].Models, model)
		if users[i].DefaultModel == "" {
			users[i].DefaultModel = model
		}
		return writeNativeProvidersFile(users)
	}
	return fmt.Errorf("native provider %q not found", id)
}

// SetUserNativeModel upserts a model on a user native provider.
func SetUserNativeModel(id, model string) error {
	if err := AddUserNativeModel(id, model); err != nil {
		if strings.Contains(err.Error(), "already exists") {
			return nil
		}
		return err
	}
	return nil
}

// RemoveUserNativeModel deletes a model; when it was the default, the first
// remaining model becomes the default.
func RemoveUserNativeModel(id, model string) error {
	users, err := LoadUserNativeProviders()
	if err != nil {
		return err
	}
	for i := range users {
		if users[i].ID != id {
			continue
		}
		filtered := users[i].Models[:0]
		removed := false
		for _, m := range users[i].Models {
			if m == model {
				removed = true
				continue
			}
			filtered = append(filtered, m)
		}
		if !removed {
			return fmt.Errorf("model %q not found for native provider %q", model, id)
		}
		users[i].Models = filtered
		if users[i].DefaultModel == model {
			if len(filtered) > 0 {
				users[i].DefaultModel = filtered[0]
			} else {
				users[i].DefaultModel = ""
			}
		}
		return writeNativeProvidersFile(users)
	}
	return fmt.Errorf("native provider %q not found", id)
}

func writeNativeProvidersFile(providers []UserNativeProvider) error {
	if err := os.MkdirAll(AixDir(), 0755); err != nil {
		return err
	}
	var buf strings.Builder
	if err := toml.NewEncoder(&buf).Encode(nativeProvidersFile{Providers: providers}); err != nil {
		return err
	}
	return writePrivateFile(NativeProvidersPath(), []byte(buf.String()))
}
