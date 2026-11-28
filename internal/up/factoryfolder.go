package up

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/JacobJNilsson/simple-factory/internal/config"
)

// FolderMode says what sfy factory-folder does when it finds no factory for the current folder.
type FolderMode int

const (
	// FindFolder prints the folder that config.Lookup finds, which is ~/.sfy without a name prefix.
	FindFolder FolderMode = iota
	// CreateFolder prints the folder that sfy up would start, and makes the factory folder when sfy up would.
	CreateFolder
	// CheckFolder prints the folder of a factory that exists, and returns ErrNoFactory when none exists.
	CheckFolder
)

// ErrNoFactory reports that sfy factory-folder --check found no factory for the current folder and its name prefix.
var ErrNoFactory = errors.New("no factory here")

// FactoryFolder runs sfy factory-folder: it prints the factory folder that the rules find for the current folder.
// The mode decides what happens when the folder has no factory. A note about another factory of the folder goes to stderr.
func FactoryFolder(env func(string) string, stdout, stderr io.Writer, mode FolderMode) error {
	var home string
	switch mode {
	case CreateFolder:
		// A wrapper's first start runs this in a folder that may have a plain factory, so it never refuses.
		made, err := factoryEnv(env, os.Getwd, stderr, false)
		if err != nil {
			return err
		}
		home = made("SFY_HOME")
	case CheckFolder:
		found, err := existingFactory(env)
		if err != nil {
			return err
		}
		home = found
	default:
		found, err := config.Lookup(env)
		if err != nil {
			return err
		}
		home = found
	}
	_, err := fmt.Fprintln(stdout, home)
	return err
}

// existingFactory returns the factory folder that config.Lookup finds, or ErrNoFactory when it finds none.
// The default factory counts as none, because Lookup falls back to it for every folder without a factory.
func existingFactory(env func(string) string) (string, error) {
	home, err := config.Lookup(env)
	var none *config.NoFactoryError
	if errors.As(err, &none) {
		return "", ErrNoFactory
	}
	if err != nil {
		return "", err
	}
	if env("SFY_HOME") == "" && home == config.DefaultHome(env("HOME")) {
		return "", ErrNoFactory
	}
	return home, nil
}
