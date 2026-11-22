package up

import (
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/process"
	"github.com/JacobJNilsson/simple-factory/internal/store"
	"github.com/JacobJNilsson/simple-factory/internal/task"
)

func TestShowConfigPrintsTheProcessWithTheFileOfEachList(t *testing.T) {
	env, _, home := configFactory(t)
	part := filepath.Join(home, "config.d", "50-gate.toml")
	writeTestFile(t, part, `
[agents.gate]
harness = "shell"
run = ["sh {factory}/checks/gate.sh {task_file}"]
permissions = { sandbox = false }

[lists.build]
agents = ["worker", "gate", "check", "reviewer"]
`)

	got := showConfig(t, env)

	for _, want := range []string{
		"\n# Process\n# start = build # default\n",
		"# list build = worker, gate, check, reviewer # " + part + "\n",
		"#   pass: if remote and hold held, if remote ship, otherwise land\n",
		"#   fail: at check build with a new round, at reviewer build, otherwise escalated\n",
		"# list held = release, push, integrate # default\n",
		"# agent gate: harness shell, ends done, run sh {factory}/checks/gate.sh {task_file}, wide: no sandbox\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
}

func TestLoadDefinitionRefusesAProcessThatRemovesAnOpenPosition(t *testing.T) {
	_, userHome, home := configFactory(t)
	held := task.Task{ID: "t1", Title: "Ship it", Description: "d", Goal: "g", State: task.Held}
	if err := store.Files(home).Create(held); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(home, "config.toml"), "[lists.held]\nagents = [\"ship\"]\n")

	_, _, err := loadDefinition(home, userHome)

	var gone *process.GoneError
	if !errors.As(err, &gone) || gone.Task != "t1" || gone.At.String() != "held/release" {
		t.Fatalf("loadDefinition = %v, want an error that names t1 at held/release", err)
	}
}

func TestShowConfigRefusesAProcessThatTheEngineCannotRun(t *testing.T) {
	env, _, home := configFactory(t)
	writeTestFile(t, filepath.Join(home, "config.toml"), "[agents.drafter]\nharness = \"claude\"\n")

	if err := ShowConfig(env, io.Discard, io.Discard); err == nil {
		t.Fatal("a claude agent with a new name loaded")
	}
}
