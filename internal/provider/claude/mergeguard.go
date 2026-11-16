package claude

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// MergeTarget is the folder that a hand merge command acts on. Unknown is true when the command names the folder with a shell expansion.
type MergeTarget struct {
	Folder  string
	Unknown bool
	// Command is the verb that the command runs, such as "git push" or "gh pr merge".
	Command string
}

// HookInput is the part of a PreToolUse hook input that the merge guard reads.
type HookInput struct {
	Cwd       string `json:"cwd"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
}

// ParseHookInput decodes the JSON that Claude Code writes to a hook's stdin.
func ParseHookInput(b []byte) (HookInput, error) {
	var in HookInput
	err := json.Unmarshal(b, &in)
	return in, err
}

var (
	segmentSplit = regexp.MustCompile(`&&|\|\||[;|\n&]`)
	expansion    = regexp.MustCompile("[$`*?]")
)

// gitHandVerbs are the git verbs that integrate a branch by hand.
var gitHandVerbs = []string{"push", "merge", "rebase"}

// MergeTargets finds the hand merge commands in a shell command line and the folder each one acts on.
// It reads cd, git -C, git --work-tree and git --git-dir. It splits on shell separators and does not run a shell.
// It does not follow a subshell, a shell function, a script, eval, bash -c, an alias, or gh --repo.
func MergeTargets(command, cwd, home string) []MergeTarget {
	var out []MergeTarget
	dir, unknown := cwd, false
	for _, seg := range segmentSplit.Split(command, -1) {
		words := commandWords(seg)
		if len(words) == 0 {
			continue
		}
		switch words[0] {
		case "cd":
			if len(words) == 2 {
				dir, unknown = resolveDir(dir, unknown, words[1], home)
			}
		case "git":
			if t, ok := gitTarget(words[1:], dir, unknown, home); ok {
				out = append(out, t)
			}
		case "gh":
			if ghMerges(words[1:]) {
				out = append(out, MergeTarget{Folder: dir, Unknown: unknown, Command: "gh pr merge"})
			}
		}
	}
	return out
}

// commandWords splits a segment into words, drops quotes, and skips leading variable assignments and wrapper commands.
func commandWords(seg string) []string {
	var words []string
	for _, w := range strings.Fields(seg) {
		words = append(words, strings.Trim(w, `"'`))
	}
	for len(words) > 0 && (strings.Contains(words[0], "=") && !strings.HasPrefix(words[0], "-") || slices.Contains([]string{"env", "command", "exec", "nohup", "time"}, words[0])) {
		words = words[1:]
	}
	return words
}

// resolveDir applies a directory word to the current directory. A word with a shell expansion makes the result unknown.
func resolveDir(dir string, unknown bool, word, home string) (string, bool) {
	if expansion.MatchString(word) {
		return dir, true
	}
	if word == "~" || strings.HasPrefix(word, "~/") {
		word = filepath.Join(home, strings.TrimPrefix(word, "~"))
	}
	if filepath.IsAbs(word) {
		return filepath.Clean(word), false
	}
	return filepath.Join(dir, word), unknown
}

// gitTarget reads the options before a git verb and reports a push, merge or rebase.
func gitTarget(words []string, dir string, unknown bool, home string) (MergeTarget, bool) {
	for i := 0; i < len(words); i++ {
		w := words[i]
		if !strings.HasPrefix(w, "-") {
			if !slices.Contains(gitHandVerbs, w) {
				return MergeTarget{}, false
			}
			return MergeTarget{Folder: dir, Unknown: unknown, Command: "git " + w}, true
		}
		name, value, joined := strings.Cut(w, "=")
		switch name {
		case "-C", "-c", "--git-dir", "--work-tree", "--namespace", "--exec-path":
			if !joined && i+1 < len(words) {
				i++
				value = words[i]
			}
			if name == "-C" || name == "--work-tree" {
				dir, unknown = resolveDir(dir, unknown, value, home)
			}
		}
	}
	return MergeTarget{}, false
}

// ghMerges reports whether gh arguments run pr merge. It skips the repo option and its value.
func ghMerges(words []string) bool {
	var rest []string
	for i := 0; i < len(words); i++ {
		switch w := words[i]; {
		case w == "-R" || w == "--repo":
			i++
		case strings.HasPrefix(w, "-"):
		default:
			rest = append(rest, w)
		}
	}
	return len(rest) >= 2 && rest[0] == "pr" && rest[1] == "merge"
}
