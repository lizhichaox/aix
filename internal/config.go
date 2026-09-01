package internal

import (
	"bytes"
	"fmt"
	"os"
	"time"

	"github.com/BurntSushi/toml"
)

type State struct {
	Apps      map[string]string `toml:"apps"`
	UpdatedAt string            `toml:"updated_at,omitempty"`
}

func LoadState() (*State, error) {
	s := &State{Apps: make(map[string]string)}
	path := StatePath()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return s, nil
	}
	_, err := toml.DecodeFile(path, s)
	if s.Apps == nil {
		s.Apps = make(map[string]string)
	}
	return s, err
}

func SaveAppState(appID, provider string) error {
	return SaveAppStates(map[string]string{appID: provider})
}

// SaveAppStates updates one or more internal app targets in a single atomic
// write. Public multi-client harnesses such as Claude use this to ensure state
// never records only half of a successful selection.
func SaveAppStates(updates map[string]string) error {
	state, err := LoadState()
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}
	for appID, provider := range updates {
		if provider == "" {
			state.Apps[appID] = "-"
		} else {
			state.Apps[appID] = provider
		}
	}
	state.UpdatedAt = time.Now().Format(time.RFC3339)
	var out bytes.Buffer
	if err := toml.NewEncoder(&out).Encode(state); err != nil {
		return err
	}
	return writePrivateFile(StatePath(), out.Bytes())
}

func EnsureDirs() error {
	dirs := []string{AixDir(), BackupsDir()}
	for _, a := range AllApps() {
		dirs = append(dirs, AppDir(a.ID))
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("create dir %s: %w", d, err)
		}
	}
	return nil
}
