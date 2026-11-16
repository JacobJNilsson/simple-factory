package claude_test

import (
	"slices"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/provider/claude"
)

func TestMergeTargetsFindHandMergesAndTheirFolder(t *testing.T) {
	tests := []struct {
		name, command string
		want          []claude.MergeTarget
	}{
		{"push in the session folder", "git push", []claude.MergeTarget{{Folder: "/state", Command: "git push"}}},
		{"push with -C", "git -C /repo push origin main", []claude.MergeTarget{{Folder: "/repo", Command: "git push"}}},
		{"relative -C", "git -C ../repo merge x", []claude.MergeTarget{{Folder: "/repo", Command: "git merge"}}},
		{"-c before the verb", "git -c a=b -C /repo rebase main", []claude.MergeTarget{{Folder: "/repo", Command: "git rebase"}}},
		{"work tree option", "git --work-tree=/repo merge x", []claude.MergeTarget{{Folder: "/repo", Command: "git merge"}}},
		{"cd then push", "cd ~/Developer/work/app && git push", []claude.MergeTarget{{Folder: "/home/u/Developer/work/app", Command: "git push"}}},
		{"quoted path", `git -C "/repo" push`, []claude.MergeTarget{{Folder: "/repo", Command: "git push"}}},
		{"gh merge", "cd /repo; gh pr merge 5 --squash", []claude.MergeTarget{{Folder: "/repo", Command: "gh pr merge"}}},
		{"variable prefix", "GIT_TERMINAL_PROMPT=0 git -C /repo push", []claude.MergeTarget{{Folder: "/repo", Command: "git push"}}},
		{"expanded folder", `git -C "$REPO" push`, []claude.MergeTarget{{Folder: "/state", Unknown: true, Command: "git push"}}},
		{"second command", "make check | tee x && git -C /a rebase main", []claude.MergeTarget{{Folder: "/a", Command: "git rebase"}}},
		{"read only git", "git -C /repo status && git log && gh pr view 5", nil},
		{"pushes in a word", "echo git push", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := claude.MergeTargets(tc.command, "/state", "/home/u")
			if !slices.Equal(got, tc.want) {
				t.Errorf("MergeTargets(%q) = %+v, want %+v", tc.command, got, tc.want)
			}
		})
	}
}

func TestMergeTargetsSkipTheRepoOptionOfGh(t *testing.T) {
	got := claude.MergeTargets("gh -R owner/repo pr merge 5", "/state", "/home/u")

	if len(got) != 1 || got[0].Command != "gh pr merge" {
		t.Errorf("got %+v", got)
	}
}
