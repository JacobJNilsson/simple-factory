package up

import (
	"fmt"
	"io"

	"github.com/JacobJNilsson/simple-factory/internal/provider/claude"
)

// InstallHooks adds sfy's hooks to the user's agent settings, so a registered agent gets them.
// It prints what it changed. The hooks act only for the registered session.
func InstallHooks(env func(string) string, stdout io.Writer) error {
	path, err := claude.UserSettingsPath(env)
	if err != nil {
		return err
	}
	bin, err := runningBinary()
	if err != nil {
		return err
	}
	return installHooks(path, bin, stdout)
}

func installHooks(path, bin string, stdout io.Writer) error {
	changes, err := claude.InstallUserHooks(path, bin)
	if err != nil {
		return err
	}
	if len(changes) == 0 {
		_, err = fmt.Fprintf(stdout, "The sfy hooks are already in %s. Nothing changed.\n", path)
		return err
	}
	if _, err := fmt.Fprintf(stdout, "Changed %s:\n", path); err != nil {
		return err
	}
	for _, c := range changes {
		if _, err := fmt.Fprintf(stdout, "- %s\n", c); err != nil {
			return err
		}
	}
	return nil
}
