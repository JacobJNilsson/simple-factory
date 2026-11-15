// Package envrun starts a command with an environment that a file carries, so no value appears in argv.
package envrun

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// Wrap puts env in a 0600 file, because argv is visible to every user in ps. NUL separates the entries, because values may hold newlines.
func Wrap(dir string, env []string, clean bool, bin string, cmd []string) ([]string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("envrun: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("envrun: %w", err)
	}
	if err := Clean(dir); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(dir, "env-*")
	if err != nil {
		return nil, fmt.Errorf("envrun: %w", err)
	}
	_, werr := f.WriteString(strings.Join(env, "\x00"))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(f.Name())
		return nil, fmt.Errorf("envrun: %w", werr)
	}
	argv := []string{bin, "run"}
	if clean {
		argv = append(argv, "-i")
	}
	argv = append(argv, f.Name(), "--")
	return append(argv, cmd...), nil
}

// Clean removes the env files that earlier Wrap calls left in dir, for example after a failed start.
func Clean(dir string) error {
	old, err := filepath.Glob(filepath.Join(dir, "env-*"))
	if err != nil {
		return fmt.Errorf("envrun: %w", err)
	}
	for _, f := range old {
		if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("envrun: %w", err)
		}
	}
	return nil
}

// Run unlinks the env file before it reads it, so no secret stays on disk. Run returns only on an error.
func Run(args []string) error {
	clean := len(args) > 0 && args[0] == "-i"
	if clean {
		args = args[1:]
	}
	if len(args) < 3 || args[1] != "--" {
		return errors.New("usage: sfy run [-i] <envfile> -- <command> [args...]")
	}
	file, cmd := args[0], args[2:]
	f, err := os.Open(file)
	if err != nil {
		return fmt.Errorf("run: %w", err)
	}
	// Unlinking before the read leaves no file behind, whatever happens next.
	if err := os.Remove(file); err != nil {
		_ = f.Close()
		return fmt.Errorf("run: %w", err)
	}
	b, err := io.ReadAll(f)
	_ = f.Close()
	if err != nil {
		return fmt.Errorf("run: %w", err)
	}
	var listed []string
	if len(b) > 0 {
		listed = strings.Split(string(b), "\x00")
	}
	env := listed
	if !clean {
		env = overlay(os.Environ(), listed)
	}
	path, err := lookPath(cmd[0], env)
	if err != nil {
		return fmt.Errorf("run: %w", err)
	}
	return fmt.Errorf("run: %w", syscall.Exec(path, cmd, env))
}

// lookPath finds name on the PATH of env, the way the shell would after env -i.
// It sets PATH for this process without a restore, because Run replaces the process next.
func lookPath(name string, env []string) (string, error) {
	if strings.Contains(name, "/") {
		return name, nil
	}
	path := ""
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, "PATH="); ok {
			path = v
		}
	}
	if err := os.Setenv("PATH", path); err != nil {
		return "", err
	}
	return exec.LookPath(name)
}

// overlay returns base with every key that extra sets replaced, so no key appears twice.
func overlay(base, extra []string) []string {
	set := map[string]bool{}
	for _, e := range extra {
		k, _, _ := strings.Cut(e, "=")
		set[k] = true
	}
	var out []string
	for _, e := range base {
		if k, _, _ := strings.Cut(e, "="); !set[k] {
			out = append(out, e)
		}
	}
	return append(out, extra...)
}
