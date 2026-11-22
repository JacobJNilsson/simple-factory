package engine_test

import (
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/process"
	"github.com/JacobJNilsson/simple-factory/internal/task"
)

// draftProcess adds a list that runs the build agents and then lands without a push.
const draftProcess = `
[lists.draft]
agents = ["build"]

[lists.draft.then]
fail.check = { list = "draft", round = true }
fail.reviewer = "draft"
pass = "land"
`

// setProcess makes the engine run the process that text merges onto the default.
func (v env) setProcess(t *testing.T, text string) {
	t.Helper()
	p, err := process.Parse("claude", text)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Validate(nil); err != nil {
		t.Fatal(err)
	}
	v.e.SetProcess(p)
}

func TestTaskNewRefusesAnUnknownList(t *testing.T) {
	v := newEnv(t)

	mustFail(t, v.run("lead", "", "task new", taskFile+"\n## List\n\ndraft\n"), "## List names \"draft\"")
}

func TestTaskNewTakesAListOfTheProcess(t *testing.T) {
	v := newEnv(t)
	v.setProcess(t, draftProcess)

	a := v.run("lead", "", "task new", taskFile+"\n## List\n\ndraft\n")

	mustOK(t, a)
	if tk := v.state(t, a.Text); tk.List != "draft" {
		t.Fatalf("List = %q, want draft", tk.List)
	}
}

func TestTaskEditKeepsTheListOfATaskWithAWorkspace(t *testing.T) {
	v := newEnv(t)
	v.setProcess(t, draftProcess)
	v.seed(t, task.Working, 0)

	mustFail(t, v.run("lead", "", "task edit", taskFile+"\n## List\n\ndraft\n", "t1"), "## List section is fixed")
}

func TestTaskEditRefusesAnUnknownList(t *testing.T) {
	v := newEnv(t)
	v.seed(t, task.Queued, 0)

	mustFail(t, v.run("lead", "", "task edit", taskFile+"\n## List\n\nnowhere\n", "t1"), "## List names \"nowhere\"")
}
