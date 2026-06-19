package registry

import (
	"fmt"
	"os"
	"path/filepath"

	_ "embed"
)

//go:embed default_registry.yaml
var defaultRegistry []byte

// DefaultYAML returns the factory-default registry shipped in the binary.
func DefaultYAML() []byte { return defaultRegistry }

// GlobalPath returns ~/.config/prismag/registry.yaml.
func GlobalPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "prismag", "registry.yaml"), nil
}

// ScaffoldGlobal writes the default registry to the global path if it does not
// already exist. Returns the path and whether it created a new file.
func ScaffoldGlobal() (string, bool, error) {
	path, err := GlobalPath()
	if err != nil {
		return "", false, err
	}
	if _, err := os.Stat(path); err == nil {
		return path, false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", false, fmt.Errorf("create config dir: %w", err)
	}
	if err := os.WriteFile(path, defaultRegistry, 0644); err != nil {
		return "", false, fmt.Errorf("write registry: %w", err)
	}
	return path, true, nil
}
