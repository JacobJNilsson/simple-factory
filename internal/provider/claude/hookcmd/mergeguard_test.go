package hookcmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// guardEnv returns an env with a user home that holds a no-merge folder "work" and a free folder "own".
func guardEnv(t *testing.T) (env func(string) string, home string) {
	t.Helper()
	home = t.TempDir()
	for _, d := range []string{"work/app", "own/app"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, "work", ".sfy.toml"), []byte("no_merge = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return envOf(map[string]string{"HOME": home, "SFY_HOME": filepath.Join(home, ".sfy")}), home
}

func guard(t *testing.T, env func(string) string, cwd, command string) (code int, errb string) {
	t.Helper()
	in, err := json.Marshal(map[string]any{"cwd": cwd, "tool_input": map[string]string{"command": command}})
	if err != nil {
		t.Fatal(err)
	}
	code, _, errb = runWith(env, []string{"merge-guard"}, string(in))
	return code, errb
}

func TestMergeGuardBlocksHandMergesInANoMergeRepo(t *testing.T) {
	env, home := guardEnv(t)
	app := filepath.Join(home, "work", "app")
	for _, command := range []string{
		"git -C " + app + " push origin main", "cd " + app + " && git merge x", "git --git-dir=x -C " + app + " rebase main",
		"cd " + app + " && gh pr merge 5 --squash", "cd ~/work/app && git push",
	} {
		code, errb := guard(t, env, home, command)

		if code != exitWake || !strings.Contains(errb, "no-merge") {
			t.Errorf("%q: code %d, stderr %q", command, code, errb)
		}
	}
}

func TestMergeGuardBlocksFromTheNoMergeFolderAndItsSubfolder(t *testing.T) {
	env, home := guardEnv(t)
	for _, cwd := range []string{filepath.Join(home, "work", "app"), filepath.Join(home, "work")} {
		for _, command := range []string{"git push", "git merge x", "git rebase main", "gh pr merge 5"} {
			code, errb := guard(t, env, cwd, command)

			if code != exitWake || !strings.Contains(errb, "no-merge") {
				t.Errorf("%q in %s: code %d, stderr %q", command, cwd, code, errb)
			}
		}
	}
}

func TestMergeGuardAllowsMergesOutsideNoMergeRepos(t *testing.T) {
	env, home := guardEnv(t)
	own := filepath.Join(home, "own", "app")

	for _, command := range []string{"git push", "git merge x", "git rebase main", "gh pr merge 5", "git status", "git -C " + own + " push"} {
		if code, errb := guard(t, env, own, command); code != exitOK || errb != "" {
			t.Errorf("%q: code %d, stderr %q", command, code, errb)
		}
	}
}

func TestMergeGuardBlocksAnotherRepoByGitC(t *testing.T) {
	env, home := guardEnv(t)

	code, _ := guard(t, env, filepath.Join(home, "own", "app"), "git -C "+filepath.Join(home, "work", "app")+" push")

	if code != exitWake {
		t.Errorf("code %d, want %d", code, exitWake)
	}
}

func TestMergeGuardBlocksWhenTheFolderIsUnknownOrTheFileBroken(t *testing.T) {
	env, home := guardEnv(t)
	if code, _ := guard(t, env, home, `git -C "$REPO" push`); code != exitWake {
		t.Errorf("expanded folder: code %d", code)
	}
	if err := os.WriteFile(filepath.Join(home, "own", ".sfy.toml"), []byte("bogus = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if code, _ := guard(t, env, filepath.Join(home, "own", "app"), "git push"); code != exitWake {
		t.Errorf("broken file: code %d", code)
	}
}

func TestMergeGuardBlocksUnreadableInputAndIgnoresOtherCommands(t *testing.T) {
	env, home := guardEnv(t)

	if code, _, _ := runWith(env, []string{"merge-guard"}, "not json"); code != exitWake {
		t.Errorf("bad input: code %d", code)
	}
	if code, _ := guard(t, env, filepath.Join(home, "work"), "make check && git status"); code != exitOK {
		t.Errorf("other command: code %d", code)
	}
}
