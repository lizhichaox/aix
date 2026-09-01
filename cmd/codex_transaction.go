package cmd

import (
	"fmt"

	"github.com/lizhichaox/aix/internal"
)

type codexConfigTransaction struct {
	files []configFileSnapshot
}

func beginCodexConfigTransaction(provider string) (*codexConfigTransaction, error) {
	paths := []string{
		internal.CodexConfigPath(),
		internal.CodexModelsPath(),
		internal.CodexNativeSnapshotPath(),
		internal.ProxyConfigPath(),
		internal.StatePath(),
		internal.ProviderPath("codex", provider),
	}
	tx := &codexConfigTransaction{}
	for _, path := range uniqueNonEmpty(paths) {
		snapshot, err := captureConfigFile(path)
		if err != nil {
			return nil, fmt.Errorf("snapshot Codex transaction: %w", err)
		}
		tx.files = append(tx.files, snapshot)
	}
	return tx, nil
}

func (tx *codexConfigTransaction) Rollback() error {
	for _, snapshot := range tx.files {
		if err := restoreConfigFile(snapshot); err != nil {
			return fmt.Errorf("Codex transaction rollback: %w", err)
		}
	}
	return nil
}
