package up

import (
	"fmt"
	"io"
	"os"

	"github.com/JacobJNilsson/simple-factory/internal/config"
)

// FactoryFolder runs sfy factory-folder: it prints the factory folder that the rules find for the current folder.
// Without create it makes nothing and prints the folder that config.Lookup finds.
// With create it prints the folder that sfy up would start, and it makes the factory folder when sfy up would.
// A note about another factory of the folder goes to stderr.
func FactoryFolder(env func(string) string, stdout, stderr io.Writer, create bool) error {
	var home string
	if create {
		// A wrapper's first start runs this in a folder that may have a plain factory, so it never refuses.
		made, err := factoryEnv(env, os.Getwd, stderr, false)
		if err != nil {
			return err
		}
		home = made("SFY_HOME")
	} else {
		found, err := config.Lookup(env)
		if err != nil {
			return err
		}
		home = found
	}
	_, err := fmt.Fprintln(stdout, home)
	return err
}
