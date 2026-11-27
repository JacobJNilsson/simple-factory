package up

import (
	"fmt"
	"strings"

	"github.com/JacobJNilsson/simple-factory/internal/config"
)

// otherPrefixError reports a folder whose factories all have other name prefixes than the command's.
// sfy up and sfy restart refuse there, because a new empty factory would hide the mistake.
type otherPrefixError struct {
	// prefix is the name prefix of the command, and others are the factory folders of the folder.
	prefix string
	others []string
}

func (e *otherPrefixError) Error() string { return otherPrefixText(e.prefix, e.others) }

// otherPrefixText names each factory folder of others with its name prefix, and how to reach it or make a factory with prefix.
func otherPrefixText(prefix string, others []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "this folder has no factory %s, only factories of other name prefixes:", describePrefix(prefix))
	for _, other := range others {
		has, run := "no name prefix", "Run sfy with "+config.NamePrefixEnv+" unset"
		if prefix := config.NamePrefixOf(other); prefix != "" {
			has, run = "the name prefix "+prefix, "Run the wrapper that sets "+config.NamePrefixEnv+"="+prefix
		}
		fmt.Fprintf(&b, "\n  %s has %s. %s, or set SFY_HOME=%s.", other, has, run, other)
	}
	fmt.Fprintf(&b, "\nRun sfy up --new-factory to make a factory %s here.", describePrefix(prefix))
	return b.String()
}

// describePrefix names a name prefix in a sentence.
func describePrefix(prefix string) string {
	if prefix == "" {
		return "without a name prefix"
	}
	return "with the name prefix " + prefix
}

// checkRestartFactory fails when sfy restart has no factory to restart.
// In a folder whose factories all have other name prefixes, it fails with an otherPrefixError. SFY_HOME skips that check.
func checkRestartFactory(env func(string) string, getwd func() (string, error)) error {
	if env("SFY_HOME") != "" {
		return nil
	}
	prefix, err := config.NamePrefix(env)
	if err != nil {
		return err
	}
	userHome := env("HOME")
	cwd, err := getwd()
	if err != nil {
		return err
	}
	if config.CanonicalPath(cwd) != config.CanonicalPath(userHome) {
		if _, ok := config.FindFactory(userHome, cwd, prefix); !ok {
			if others := config.FactoriesAt(userHome, cwd); len(others) > 0 {
				return &otherPrefixError{prefix: prefix, others: others}
			}
		}
	}
	_, err = config.Lookup(env)
	return err
}
