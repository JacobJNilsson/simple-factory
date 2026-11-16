package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// TrustWarning returns a warning when Claude Code would show its trust dialog for dir, or when sfy cannot tell.
// It only reads Claude Code's config file, so sfy never changes the provider's own config.
func (a adapter) TrustWarning(dir string) string {
	trusted, err := trusts(dir)
	switch {
	case err != nil:
		return fmt.Sprintf("sfy cannot tell whether Claude Code trusts %s (%v). If an agent stops at a trust dialog, open that agent's terminal and accept the dialog.", dir, err)
	case trusted:
		return ""
	case !exists(dir):
		return fmt.Sprintf("Claude Code does not trust %s, which sfy creates later, so an agent will stop at the trust dialog there. Open that agent's terminal and accept the dialog.", dir)
	}
	return fmt.Sprintf("Claude Code does not trust %s yet, so an agent there will stop at its trust dialog. Run claude in %s once and accept, or open that agent's terminal and accept the dialog.", dir, dir)
}

func exists(dir string) bool {
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}

// trusts reports whether Claude Code has dir's trust key in its config file.
// The error says why the answer is unknown, and it never holds content of the file.
func trusts(dir string) (bool, error) {
	accepted, err := readTrust()
	if err != nil {
		return false, err
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return false, errors.New("the folder has no absolute path")
	}
	keys := []string{dir}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil && resolved != dir {
		keys = append(keys, resolved)
	}
	for _, key := range keys {
		if root, ok := repoRoot(key); ok {
			// Trust of a parent folder never covers a repository.
			return accepted[root], nil
		}
	}
	if !exists(dir) {
		// A folder that does not exist yet becomes a clone, which is a repository.
		return accepted[dir], nil
	}
	for _, key := range keys {
		for p := key; ; p = filepath.Dir(p) {
			if accepted[p] {
				return true, nil
			}
			if filepath.Dir(p) == p {
				break
			}
		}
	}
	return false, nil
}

// readTrust returns the folders with hasTrustDialogAccepted set, from the projects key of Claude Code's config file.
func readTrust() (map[string]bool, error) {
	path, err := configPath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%s does not exist", path)
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read %s", path)
	}
	var file struct {
		Projects map[string]struct {
			Accepted bool `json:"hasTrustDialogAccepted"`
		} `json:"projects"`
	}
	if json.Unmarshal(b, &file) != nil {
		return nil, fmt.Errorf("cannot parse %s", path)
	}
	accepted := map[string]bool{}
	for key, project := range file.Projects {
		if project.Accepted {
			accepted[key] = true
		}
	}
	return accepted, nil
}

// configPath assumes that CLAUDE_CONFIG_DIR/.claude.json is the file when the variable is set, which the docs do not state.
func configPath() (string, error) {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, ".claude.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.New("no home folder")
	}
	return filepath.Join(home, ".claude.json"), nil
}

// repoRoot returns the root that Claude Code keys trust on: the main checkout of the repository that holds dir.
// A worktree's git common dir is the main checkout's .git folder, so its parent is the root.
func repoRoot(dir string) (string, bool) {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return "", false
	}
	common, ok := gitOut(dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if !ok {
		return "", false
	}
	if filepath.Base(common) == ".git" {
		return filepath.Dir(common), true
	}
	// A submodule or a bare repository has no main checkout above its git folder.
	return gitOut(dir, "rev-parse", "--show-toplevel")
}

// gitTimeout bounds one git call, so a stuck repository cannot stall sfy up.
const gitTimeout = 5 * time.Second

func gitOut(dir string, args ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	// A GIT_DIR from the host would point git at another repository.
	cmd.Env = slices.DeleteFunc(os.Environ(), func(e string) bool {
		return strings.HasPrefix(e, "GIT_DIR=") || strings.HasPrefix(e, "GIT_WORK_TREE=")
	})
	out, err := cmd.Output()
	line := strings.TrimSpace(string(out))
	return line, err == nil && line != ""
}
