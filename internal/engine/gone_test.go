package engine_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/task"
)

// goneNotes returns the bodies of the lead's messages that say a session is gone.
func (v env) goneNotes(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, m := range v.inbox(t, "lead") {
		if strings.Contains(m.Body, "is gone") {
			out = append(out, m.Body)
		}
	}
	return out
}

// tickThrice ticks as often as an alive check may fail before sfy tells the lead.
func (v env) tickThrice(t *testing.T) {
	t.Helper()
	for range 3 {
		v.tick(t)
	}
}

func TestTheLeadHearsOnceThatAWorkerIsGone(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Working, 1)
	v.l.dead = map[string]bool{"worker-t1": true}

	v.tickThrice(t)

	want := "The worker of task t1 is gone. Its session no longer runs."
	if got := v.goneNotes(t); len(got) != 1 || got[0] != want {
		t.Fatalf("the lead got %q", got)
	}
}

func TestTheLeadHearsOnceThatAReviewerIsGone(t *testing.T) {
	v := newEnv(t)
	v.firstReview(t)
	v.l.dead = map[string]bool{"review-t1-1": true}

	v.tickThrice(t)

	want := "The reviewer of task t1 is gone. Its session no longer runs."
	if got := v.goneNotes(t); len(got) != 1 || got[0] != want {
		t.Fatalf("the lead got %q", got)
	}
	if got := v.state(t, "t1").State; got != task.Review {
		t.Fatalf("got %s", got)
	}
}

func TestAReviewerThatRunsAgainIsToldAgainWhenItIsGone(t *testing.T) {
	v := newEnv(t)
	v.firstReview(t)
	v.l.dead = map[string]bool{"review-t1-1": true}
	v.tickThrice(t)

	v.l.dead = nil
	v.tick(t)
	v.l.dead = map[string]bool{"review-t1-1": true}
	v.tick(t)

	if got := v.goneNotes(t); len(got) != 2 {
		t.Fatalf("the lead got %q", got)
	}
}

func TestATaskThatWaitsForAPushReportsNoGoneReviewer(t *testing.T) {
	v := newEnv(t)
	v.firstReview(t)
	mustOK(t, v.run("review-t1-1", "t1", "verdict pass", "Looks right."))
	if !slices.Contains(v.l.stopped, "review-t1-1") {
		t.Fatalf("the reviewer kept running: %v", v.l.stopped)
	}
	if tk := v.state(t, "t1"); tk.State != task.Review || !tk.Pushing {
		t.Fatalf("got %+v", tk)
	}
	v.l.dead = map[string]bool{"review-t1-1": true}

	v.tickThrice(t)

	if got := v.goneNotes(t); len(got) != 0 {
		t.Fatalf("the lead got %q", got)
	}
}

func TestTheLeadHearsThatSfyCannotTellWhetherAReviewerRuns(t *testing.T) {
	v := newEnv(t)
	v.firstReview(t)
	v.l.aliveErrOf = map[string]error{"review-t1-1": errors.New("no answer")}

	v.tickThrice(t)

	var got []string
	for _, m := range v.inbox(t, "lead") {
		if strings.Contains(m.Body, "cannot tell") {
			got = append(got, m.Body)
		}
	}
	if len(got) != 1 || got[0] != "sfy cannot tell whether review-t1-1 runs: no answer" {
		t.Fatalf("the lead got %q", got)
	}
}
