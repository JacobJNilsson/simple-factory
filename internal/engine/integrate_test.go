package engine_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/task"
)

// approvedRepoTask runs a repo task through a passed review and a push, so it waits for main.
func (v env) approvedRepoTask(t *testing.T) {
	t.Helper()
	v.firstReview(t)
	mustOK(t, v.run("review-t1-1", "t1", "verdict pass", "Good."))
	mustOK(t, v.run("worker-t1", "t1", "report pushed", "Pushed."))
	v.inbox(t, "main")
}

func TestIntegrateRunsTheGitStepsAndCompletesTheTask(t *testing.T) {
	v := newEnv(t)
	v.approvedRepoTask(t)

	mustOK(t, v.run("main", "", "task integrate", "", "t1"))

	if !slices.Equal(v.ws.integrated, []string{"t1"}) || v.state(t, "t1").State != task.Done {
		t.Errorf("integrated %v, state %s", v.ws.integrated, v.state(t, "t1").State)
	}
}

func TestIntegrateRefusesInANoMergeRepoAndNamesTheBranch(t *testing.T) {
	v := newEnv(t)
	v.e.SetRepoConfig(func(string) (config.Config, error) { return config.Config{NoMerge: true}, nil })
	v.approvedRepoTask(t)

	a := v.run("main", "", "task integrate", "", "t1")

	mustFail(t, a, "Leave branch feat/t1-title for the operator")
	if len(v.ws.integrated) != 0 || v.state(t, "t1").State != task.Approved {
		t.Errorf("integrated %v, state %s", v.ws.integrated, v.state(t, "t1").State)
	}
}

func TestIntegrateRefusesWhenTheRepoConfigCannotBeRead(t *testing.T) {
	v := newEnv(t)
	v.e.SetRepoConfig(func(string) (config.Config, error) { return config.Config{}, errors.New("bad file") })
	v.approvedRepoTask(t)

	a := v.run("main", "", "task integrate", "", "t1")

	mustFail(t, a, "bad file")
	if len(v.ws.integrated) != 0 {
		t.Errorf("integrated %v", v.ws.integrated)
	}
}

func TestAFailedIntegrationLeavesTheTaskApproved(t *testing.T) {
	v := newEnv(t)
	v.ws.integrateErr = errors.New("rebase conflict")
	v.approvedRepoTask(t)

	mustFail(t, v.run("main", "", "task integrate", "", "t1"), "rebase conflict")

	if v.state(t, "t1").State != task.Approved {
		t.Errorf("state = %s", v.state(t, "t1").State)
	}
}

func TestIntegrateNeedsAnApprovedTaskWithARepo(t *testing.T) {
	noRepo := newEnv(t)
	noRepo.seed(t, task.Approved, 1)
	working := newEnv(t)
	working.seedRepo(t, task.Working, 1)

	mustFail(t, noRepo.run("main", "", "task integrate", "", "t1"), "no repo")
	mustFail(t, working.run("main", "", "task integrate", "", "t1"), "approved")
}

func TestThePushNoteAsksForADraftPRWhenDraftPRIsSet(t *testing.T) {
	cases := []struct {
		name string
		cfg  func(string) (config.Config, error)
		want bool
	}{
		{"default", nil, false},
		{"off", func(string) (config.Config, error) { return config.Config{}, nil }, false},
		{"on", func(string) (config.Config, error) { return config.Config{DraftPR: true}, nil }, true},
		{"unreadable", func(string) (config.Config, error) { return config.Config{}, errors.New("bad file") }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := newEnv(t)
			if c.cfg != nil {
				v.e.SetRepoConfig(c.cfg)
			}
			v.firstReview(t)
			mustOK(t, v.run("review-t1-1", "t1", "verdict pass", "Good."))

			notes := v.pushNotes(t)

			if len(notes) != 1 {
				t.Fatalf("notes = %v", notes)
			}
			if got := strings.Contains(notes[0], "--draft"); got != c.want {
				t.Errorf("--draft in note = %v, want %v:\n%s", got, c.want, notes[0])
			}
		})
	}
}

func TestTheApprovalNoteSaysToIntegrateOrToLeaveTheBranch(t *testing.T) {
	cases := []struct {
		name  string
		cfg   func(string) (config.Config, error)
		wants []string
	}{
		{"merge", nil, []string{"sfy task integrate t1"}},
		{
			"no-merge",
			func(string) (config.Config, error) { return config.Config{NoMerge: true}, nil },
			[]string{"set to no-merge", "do not integrate", "Leave branch feat/t1-title", "for the operator"},
		},
		{
			"unreadable",
			func(string) (config.Config, error) { return config.Config{}, errors.New("bad file") },
			[]string{"set to no-merge", "bad file"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := newEnv(t)
			if c.cfg != nil {
				v.e.SetRepoConfig(c.cfg)
			}
			v.firstReview(t)
			mustOK(t, v.run("review-t1-1", "t1", "verdict pass", "Good."))
			mustOK(t, v.run("worker-t1", "t1", "report pushed", "Pushed."))

			notes := v.inbox(t, "main")

			if len(notes) != 1 {
				t.Fatalf("notes = %v", notes)
			}
			for _, want := range c.wants {
				if !strings.Contains(notes[0].Body, want) {
					t.Errorf("the note lacks %q:\n%s", want, notes[0].Body)
				}
			}
		})
	}
}
