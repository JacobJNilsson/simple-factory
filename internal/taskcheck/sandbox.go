package taskcheck

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Sandbox holds the path lists of a worker's sandbox. A check runs under the same lists.
type Sandbox struct {
	// DenyRead lists the paths that the command may not read. A leading ~/ means the home folder.
	DenyRead []string
	// AllowWrite lists the paths that the command may write besides its workspace and its temp folder.
	AllowWrite []string
}

// Wrap returns the argv that runs argv in dir under the sandbox. tmp is the one temp folder that the command may write.
// It uses bwrap on Linux and sandbox-exec on macOS. The command has no network limit.
func (s Sandbox) Wrap(goos, dir, tmp string, argv []string) ([]string, error) {
	write := existing(append([]string{dir, tmp, worktreeGitDir(dir)}, s.AllowWrite...))
	deny := existing(s.DenyRead)
	switch goos {
	case "linux":
		return bwrapArgv(dir, write, deny, argv), nil
	case "darwin":
		return append([]string{"sandbox-exec", "-p", seatbeltProfile(write, deny), "--"}, argv...), nil
	}
	return nil, fmt.Errorf("a sandboxed check is not supported on %s", goos)
}

// bwrapArgv mounts the root read-only, makes the write paths writable, and hides the denied paths.
func bwrapArgv(dir string, write, deny, argv []string) []string {
	out := []string{"bwrap", "--die-with-parent", "--unshare-user", "--unshare-pid", "--ro-bind", "/", "/", "--dev", "/dev", "--proc", "/proc"}
	for _, p := range write {
		out = append(out, "--bind", p, p)
	}
	for _, p := range deny {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			out = append(out, "--ro-bind", "/dev/null", p)
		} else {
			out = append(out, "--tmpfs", p)
		}
	}
	return append(append(out, "--chdir", dir, "--"), argv...)
}

// seatbeltProfile allows everything except writes outside the write paths and reads of the denied paths.
func seatbeltProfile(write, deny []string) string {
	var b strings.Builder
	b.WriteString("(version 1)\n(allow default)\n(deny file-write*)\n")
	b.WriteString(`(allow file-write* (literal "/dev/null") (literal "/dev/tty") (literal "/dev/dtracehelper") (regex #"^/dev/(fd|ttys?)[0-9]*$"))` + "\n")
	for _, p := range write {
		fmt.Fprintf(&b, "(allow file-write* (subpath %s))\n", seatbeltString(p))
	}
	for _, p := range deny {
		fmt.Fprintf(&b, "(deny file-read* (subpath %s))\n", seatbeltString(p))
	}
	return b.String()
}

func seatbeltString(p string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(p) + `"`
}

// existing returns the paths that exist, with ~/ expanded and symbolic links resolved.
// A sandbox cannot bind a missing path, and macOS names a folder by its resolved path.
func existing(paths []string) []string {
	var out []string
	for _, p := range paths {
		if rest, ok := strings.CutPrefix(p, "~/"); ok {
			home, err := os.UserHomeDir()
			if err != nil {
				continue
			}
			p = filepath.Join(home, rest)
		}
		if p == "" {
			continue
		}
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			out = append(out, resolved)
		}
	}
	return out
}

// worktreeGitDir returns the git folder that a linked worktree names in its .git file, or "" for any other folder.
// It reads the file and runs no git, because the worker controls the repo's git settings.
func worktreeGitDir(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, ".git"))
	if err != nil {
		return ""
	}
	path, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir: ")
	if !ok {
		return ""
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	return path
}
