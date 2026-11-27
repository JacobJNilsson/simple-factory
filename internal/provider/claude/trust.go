package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// TrustWarning returns a warning when Claude Code would show its trust dialog for dir, or when sfy cannot tell.
// It only reads Claude Code's config file and never changes it.
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
// A non-empty legacy .config.json in Claude Code's home folder wins, because Claude Code reads that file first.
func configPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.New("no home folder")
	}
	claudeHome, dir := filepath.Join(home, ".claude"), home
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		claudeHome, dir = d, d
	}
	legacy := filepath.Join(claudeHome, ".config.json")
	if info, err := os.Stat(legacy); err == nil && info.Mode().IsRegular() && info.Size() > 0 {
		return legacy, nil
	}
	return filepath.Join(dir, ".claude.json"), nil
}

// Trust marks dir as trusted in Claude Code's config file, so a session there shows no trust dialog.
// It writes the key that Claude Code writes when the operator accepts the dialog.
func (a adapter) Trust(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return errors.New("the folder has no absolute path")
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return fmt.Errorf("cannot resolve %s", abs)
	}
	path, err := configPath()
	if err != nil {
		return err
	}
	return markTrusted(path, trustKey(resolved))
}

// trustKey returns the folder that Claude Code marks when the operator accepts the dialog in dir.
// That is the main checkout for a folder in a repository, and dir itself otherwise.
func trustKey(dir string) string {
	if root, ok := repoRoot(dir); ok {
		return root
	}
	return dir
}

// newProject is the entry that Claude Code writes for a folder with no entry when the operator accepts the dialog.
const newProject = `{"allowedTools":[],"mcpContextUris":[],"mcpServers":{},"enabledMcpjsonServers":[],"disabledMcpjsonServers":[],` +
	`"hasTrustDialogAccepted":true,"hasClaudeMdExternalIncludesApproved":false,"hasClaudeMdExternalIncludesWarningShown":false}`

// markTrusted sets hasTrustDialogAccepted for key in the config file at path, under Claude Code's own lock.
// It keeps every other value and writes nothing when the key is already set.
func markTrusted(path, key string) error {
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("%s does not exist", path)
	case err != nil:
		return fmt.Errorf("cannot read %s", path)
	case info.Mode()&os.ModeSymlink != 0:
		return fmt.Errorf("%s is a symbolic link", path)
	}
	unlock, err := lockConfig(path)
	if err != nil {
		return err
	}
	defer unlock()
	// Claude Code may have changed the file before sfy took the lock, so sfy reads it again under the lock.
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cannot read %s", path)
	}
	out, changed, err := withTrust(b, key)
	if err != nil {
		return fmt.Errorf("cannot parse %s", path)
	}
	if !changed {
		return nil
	}
	return replaceFile(path, out, info.Mode().Perm())
}

// withTrust returns the config b with key trusted, and whether that changed anything.
// Every value other than the trust flag keeps its raw bytes.
func withTrust(b []byte, key string) ([]byte, bool, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(b, &top); err != nil || top == nil {
		return nil, false, errors.New("the config is no JSON object")
	}
	projects := map[string]json.RawMessage{}
	if raw, ok := top["projects"]; ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, &projects); err != nil || projects == nil {
			return nil, false, errors.New("projects is no JSON object")
		}
	}
	entry := json.RawMessage(newProject)
	if raw, ok := projects[key]; ok {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
			return nil, false, errors.New("the project entry is no JSON object")
		}
		if string(fields["hasTrustDialogAccepted"]) == "true" {
			return nil, false, nil
		}
		fields["hasTrustDialogAccepted"] = json.RawMessage("true")
		var err error
		if entry, err = json.Marshal(fields); err != nil {
			return nil, false, err
		}
	}
	projects[key] = entry
	raw, err := json.Marshal(projects)
	if err != nil {
		return nil, false, err
	}
	top["projects"] = raw
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(top); err != nil {
		return nil, false, err
	}
	return out.Bytes(), true, nil
}

// replaceFile writes b to a temporary file beside path and renames it over path, so a reader never sees half a file.
func replaceFile(path string, b []byte, mode os.FileMode) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".sfy-*")
	if err != nil {
		return fmt.Errorf("cannot write beside %s", path)
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	_, err = tmp.Write(b)
	if err == nil {
		err = tmp.Chmod(mode)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("cannot write beside %s", path)
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("cannot replace %s", path)
	}
	return nil
}

// errConfigLocked reports a config lock that another process held for the whole wait.
var errConfigLocked = errors.New("another process holds the lock on the Claude Code config")

// lockWait bounds the wait for the config lock. Tests shorten it.
var lockWait = 5 * time.Second

// lockConfig takes the lock that Claude Code takes on its config file: a folder named path.lock.
// It never removes a lock that another process holds, even a stale one, because Claude Code clears its own stale lock.
func lockConfig(path string) (unlock func(), err error) {
	lock := path + ".lock"
	deadline := time.Now().Add(lockWait)
	for {
		err := os.Mkdir(lock, 0o700)
		if err == nil {
			return func() { _ = os.Remove(lock) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("cannot lock %s", path)
		}
		if time.Now().After(deadline) {
			return nil, errConfigLocked
		}
		// The jitter keeps two waiting processes from trying at the same moment.
		time.Sleep(50*time.Millisecond + time.Duration(rand.IntN(25))*time.Millisecond)
	}
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

// gitTimeout bounds one git call, so a stuck repository cannot stall the sfy start.
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
