package cli

import (
	"strings"
	"testing"
)

func TestParseReadsTaskReleaseWithItsTaskId(t *testing.T) {
	req, err := parse([]string{"task", "release", "t4"}, func(string) string { return "" }, strings.NewReader(""))
	if err != nil || req.Command != "task release" || len(req.Args) != 1 || req.Args[0] != "t4" {
		t.Fatalf("parse = %+v, %v, want task release t4", req, err)
	}
}

func TestEverySessionThatSfyStartedForATaskIsRestricted(t *testing.T) {
	for agent, want := range map[string]bool{"worker-t4": true, "review-t4-2": true, "loader-t4": true, "lead": false, "": false} {
		env := func(name string) string {
			if name == "SFY_AGENT" {
				return agent
			}
			return ""
		}
		if got := Restricted(env); got != want {
			t.Errorf("Restricted(%q) = %v, want %v", agent, got, want)
		}
	}
}
