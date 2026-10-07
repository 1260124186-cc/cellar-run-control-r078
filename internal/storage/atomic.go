package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func writeSnapshot(path string, snapshot any) error {
	directory := filepath.Dir(path)
	handle, err := os.CreateTemp(directory, ".state-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary state: %w", err)
	}
	tempName := handle.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tempName)
		}
	}()

	encoder := json.NewEncoder(handle)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(snapshot); err != nil {
		_ = handle.Close()
		return fmt.Errorf("encode state: %w", err)
	}
	if err := handle.Sync(); err != nil {
		_ = handle.Close()
		return fmt.Errorf("commit state bytes: %w", err)
	}
	if err := handle.Close(); err != nil {
		return fmt.Errorf("close temporary state: %w", err)
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("replace state: %w", err)
	}
	removeTemp = false
	return nil
}
