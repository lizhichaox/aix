package internal

import (
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
	state, err := LoadState()
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}
	if provider == "" {
		state.Apps[appID] = "-"
	} else {
		state.Apps[appID] = provider
	}
	state.UpdatedAt = time.Now().Format(time.RFC3339)
	f, err := os.Create(StatePath())
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(state)
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
