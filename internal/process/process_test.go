package process_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/process"
)

// load decodes a factory's TOML text and merges it onto the default.
func load(t *testing.T, text string) process.Process {
	t.Helper()
	p, err := process.Parse("claude", text)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDefaultValidates(t *testing.T) {
	if err := process.Default().Validate(nil); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultPushCopiesTheWorker(t *testing.T) {
	push := process.Default().Agents["push"]
	if push.Harness != process.Claude || push.SessionName != "worker" || push.Prompt != "worker" || !push.Permissions.Push || push.Ends != process.EndsPushed {
		t.Fatalf("push = %+v, want the worker's claude session with push rules that ends at pushed", push)
	}
}

func TestFlattenJoinsListsAtEveryDepth(t *testing.T) {
	got, err := process.Default().Flatten("held")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"release", "push", "integrate"}; !slices.Equal(got, want) {
		t.Fatalf("Flatten(held) = %v, want %v", got, want)
	}
}

func TestFlattenRefusesAJoinThatContainsItself(t *testing.T) {
	p := load(t, `
[lists.a]
agents = ["worker", "b"]
[lists.b]
agents = ["a"]
`)
	_, err := p.Flatten("a")
	var join *process.JoinError
	if !errors.As(err, &join) || !slices.Equal(join.Path, []string{"a", "b", "a"}) {
		t.Fatalf("Flatten(a) = %v, want a join error through a, b and a", err)
	}
}

func TestNextPicksTheListByFacts(t *testing.T) {
	cases := []struct {
		facts process.Facts
		want  process.Position
	}{
		{process.Facts{Repo: true, Remote: true, Hold: true}, process.Position{List: "held", Agent: "release"}},
		{process.Facts{Repo: true, Remote: true}, process.Position{List: "ship", Agent: "push"}},
		{process.Facts{Repo: true}, process.Position{List: "land", Agent: "integrate"}},
		{process.Facts{}, process.Position{List: "land", Agent: "integrate"}},
	}
	for _, c := range cases {
		got, _, err := process.Default().Next(process.Position{List: "build", Agent: "reviewer"}, process.Pass, c.facts, "")
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("Next with %+v = %v, want %v", c.facts, got, c.want)
		}
	}
}

func TestNextGoesOnInsideAList(t *testing.T) {
	got, target, err := process.Default().Next(process.Position{List: "build", Agent: "worker"}, process.Pass, process.Facts{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != (process.Position{List: "build", Agent: "check"}) || target != (process.Target{}) {
		t.Fatalf("Next = %v %v, want build/check and no target", got, target)
	}
}

func TestNextFailsByAgent(t *testing.T) {
	p := process.Default()
	_, check, err := p.Next(process.Position{List: "build", Agent: "check"}, process.Fail, process.Facts{}, "")
	if err != nil {
		t.Fatal(err)
	}
	_, review, err := p.Next(process.Position{List: "build", Agent: "reviewer"}, process.Fail, process.Facts{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if check != (process.Target{List: "build", Round: true}) || review != (process.Target{List: "build"}) {
		t.Fatalf("fail targets = %v and %v, want build with a round and build without", check, review)
	}
}

func TestNextEndsAtDone(t *testing.T) {
	_, target, err := process.Default().Next(process.Position{List: "land", Agent: "integrate"}, process.Pass, process.Facts{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if target.List != process.Done {
		t.Fatalf("target = %v, want done", target)
	}
}

const routed = `
[agents.route]
harness = "shell"
run = ["sh route.sh"]

[lists.other]
agents = ["integrate"]

[lists.build.then]
fail.route = "escalated"
pass = { agent = "route", to = ["land", "other"] }
`

func TestNextFollowsTheShellBranch(t *testing.T) {
	got, _, err := load(t, routed).Next(process.Position{List: "build", Agent: "reviewer"}, process.Pass, process.Facts{}, "checking\nother\n")
	if err != nil {
		t.Fatal(err)
	}
	if got != (process.Position{List: "other", Agent: "integrate"}) {
		t.Fatalf("Next = %v, want other/integrate", got)
	}
}

func TestNextShellBranchOutsideToFollowsFail(t *testing.T) {
	_, target, err := load(t, routed).Next(process.Position{List: "build", Agent: "reviewer"}, process.Pass, process.Facts{}, "production\n")
	if err != nil {
		t.Fatal(err)
	}
	if target.List != process.Escalated {
		t.Fatalf("target = %v, want the fail target escalated", target)
	}
}

func TestAGateJoinsTheDefaultBuild(t *testing.T) {
	p := load(t, `
[agents.gate]
harness = "shell"
run = ["sh {factory}/checks/gate.sh {task_file}"]
show = "{from} (gate runs)"

[lists.build]
agents = ["worker", "gate", "check", "reviewer"]

[lists.build.then]
fail.gate = { list = "build", round = true }
`)
	if err := p.Validate(nil); err != nil {
		t.Fatal(err)
	}
	_, target, err := p.Next(process.Position{List: "build", Agent: "reviewer"}, process.Fail, process.Facts{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if target != (process.Target{List: "build"}) {
		t.Fatalf("the merge lost fail.reviewer: target = %v", target)
	}
}

func TestValidateRefusesARemovedPosition(t *testing.T) {
	p := load(t, `
[lists.held]
agents = ["ship"]
`)
	at := process.Position{List: "held", Agent: "release"}
	err := p.Validate(map[string]process.Position{"t4": at})
	var gone *process.GoneError
	if !errors.As(err, &gone) || gone.Task != "t4" || gone.At != at {
		t.Fatalf("Validate = %v, want an error that names t4 at held/release", err)
	}
}

func TestValidateRefuses(t *testing.T) {
	cases := map[string]string{
		"a run key on a claude agent": `
[agents.worker]
run = ["true"]`,
		"an unknown harness": `
[agents.gate]
harness = "robot"`,
		"an unknown agent in a list": `
[lists.build]
agents = ["worker", "nobody", "reviewer"]`,
		"an unknown target": `
[lists.land.then]
pass = "nowhere"`,
		"an unknown fact": `
[lists.build.then]
pass = [{ if = "sunny", list = "land" }, { list = "land" }]`,
		"a name for an agent and a list": `
[lists.worker]
agents = ["check"]`,
		"a bad name": `
[agents.Gate]
harness = "shell"
run = ["true"]`,
		"a shell branch to a non-target": `
[agents.route]
harness = "shell"
run = ["true"]
[lists.land.then]
pass = { agent = "route", to = ["nowhere"] }`,
		"a loop without a round": `
[agents.spin]
harness = "shell"
run = ["true"]
[lists.looping]
agents = ["spin"]
[lists.looping.then]
pass = "looping"`,
	}
	for name, text := range cases {
		p, err := process.Parse("claude", text)
		if err == nil {
			err = p.Validate(nil)
		}
		if err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestValidateAcceptsALoopWithARound(t *testing.T) {
	p := load(t, `
[agents.spin]
harness = "shell"
run = ["true"]
[lists.looping]
agents = ["spin"]
[lists.looping.then]
pass = { list = "looping", round = true }`)
	if err := p.Validate(nil); err != nil {
		t.Fatal(err)
	}
}

func TestExpandQuotesPaths(t *testing.T) {
	got := process.Expand("sh {factory}/gate.sh {task_file} && {task_check}", process.Values{
		TaskCheck: "make check", Factory: "/f", TaskFile: "/f/tasks/t1/it's.md",
	})
	if want := `sh '/f'/gate.sh '/f/tasks/t1/it'\''s.md' && make check`; got != want {
		t.Fatalf("Expand = %q, want %q", got, want)
	}
}

func TestCommandsSkipAnEmptyTaskCheck(t *testing.T) {
	check := process.Default().Agents["check"]
	if got := check.Commands(process.Values{}); len(got) != 0 {
		t.Fatalf("Commands = %v, want none for a task with no check", got)
	}
	if got := check.Commands(process.Values{TaskCheck: "go test ./..."}); !slices.Equal(got, []string{"go test ./..."}) {
		t.Fatalf("Commands = %v, want the check", got)
	}
}
