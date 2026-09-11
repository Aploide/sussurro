package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// configSaveMu serializes read-modify-write settings updates so two controls
// cannot overwrite one another when their bridge calls overlap.
var configSaveMu sync.Mutex

// configPath returns the file that supplied cfg, falling back to the standard
// user path for programmatically constructed configurations.
func configPath(cfg *Config) (string, error) {
	if cfg != nil && cfg.sourcePath != "" {
		return cfg.sourcePath, nil
	}
	return userConfigPath()
}

// writeConfigAtomically replaces a config file only after the complete new
// contents have reached a temporary file in the same directory. A failed write
// therefore leaves the configuration the process loaded intact.
func writeConfigAtomically(path string, data []byte) (err error) {
	// Replacing the link itself would disconnect a config managed by Stow or
	// another dotfile tool from its source. Resolve the full path first so the
	// atomic rename updates the loaded target and leaves the link in place.
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}

	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	closed := false
	defer func() {
		if !closed {
			_ = temporary.Close()
		}
		_ = os.Remove(temporaryPath)
	}()

	if err := temporary.Chmod(info.Mode().Perm()); err != nil {
		return fmt.Errorf("preserve permissions: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	closed = true
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return nil
}
