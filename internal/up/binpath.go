package up

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// binary returns the path that the agents' settings.json files name for the sfy binary.
// It prefers the path sfy started as, because a brew symlink such as /opt/homebrew/bin/sfy survives brew upgrade and the Cellar path does not.
// It falls back to the executable path when arg0 names another file.
func binary(arg0 string, executable func() (string, error), lookPath func(string) (string, error)) (string, error) {
	exe, err := executable()
	if err != nil {
		return "", err
	}
	candidate := arg0
	if !strings.ContainsRune(arg0, filepath.Separator) {
		if lookPath == nil {
			return exe, nil
		}
		if candidate, err = lookPath(arg0); err != nil {
			return exe, nil
		}
	}
	if candidate, err = filepath.Abs(candidate); err != nil {
		return exe, nil
	}
	if !sameFile(candidate, exe) {
		return exe, nil
	}
	return candidate, nil
}

func sameFile(a, b string) bool {
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	return err == nil && os.SameFile(ai, bi)
}

// runningBinary is binary for the running process.
func runningBinary() (string, error) {
	return binary(os.Args[0], os.Executable, exec.LookPath)
}
