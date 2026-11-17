package config

import "path/filepath"

// LoadWithTempLookup loads the config with a replacement for the macOS temp folder lookup.
func LoadWithTempLookup(home string, userTempDir func() (string, error)) (Config, error) {
	return load(userTempDir, filepath.Join(home, "config.toml"))
}
