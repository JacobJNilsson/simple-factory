package config

import "path/filepath"

// BothKeysError lets the tests match the error of a file that sets a key under both names.
type BothKeysError = bothKeysError

// LoadWithTempLookup loads the config with a replacement for the macOS temp folder lookup.
func LoadWithTempLookup(home string, userTempDir func() (string, error)) (Config, error) {
	return load(userTempDir, filepath.Join(home, "config.toml"))
}

// OverridesAs lists the overrides as the user with the id uid sees them, so a test can name an owner that it cannot create.
func OverridesAs(home, userHome string, uid int) ([]Override, error) {
	return overrides(home, userHome, uid)
}
