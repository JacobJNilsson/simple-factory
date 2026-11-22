package store_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/process"
	"github.com/JacobJNilsson/simple-factory/internal/store"
	"github.com/JacobJNilsson/simple-factory/internal/task"
)

// positionKeys holds the keys of state.json that place a task. A nil pointer is a key that the file lacks.
type positionKeys struct {
	State string  `json:"state"`
	List  *string `json:"list"`
	Agent *string `json:"agent"`
}

// stateKeys reads the keys of a task's state.json that place the task.
func stateKeys(t *testing.T, home, id string) positionKeys {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, "tasks", id, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var keys positionKeys
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatal(err)
	}
	return keys
}

func TestAPositionOfItsOwnSurvivesASaveAndLoad(t *testing.T) {
	home := t.TempDir()
	s := store.Files(home)
	in := sample("t1", task.Review)
	in.At = process.Position{List: "load", Agent: "auditor"}
	if err := s.Create(in); err != nil {
		t.Fatal(err)
	}

	got, err := s.Task("t1")

	if err != nil || got.Position() != in.At {
		t.Fatalf("Position = %v, %v, want %v", got.Position(), err, in.At)
	}
	if keys := stateKeys(t, home, "t1"); keys.List == nil || *keys.List != "load" || keys.Agent == nil || *keys.Agent != "auditor" {
		t.Fatalf("state.json = %+v, want the list load and the agent auditor", keys)
	}
}

func TestADefaultPositionKeepsTheStateFieldsOnly(t *testing.T) {
	home := t.TempDir()
	s := store.Files(home)
	in := sample("t1", task.Review)
	in.At = process.Position{List: "build", Agent: "reviewer"}
	if err := s.Create(in); err != nil {
		t.Fatal(err)
	}

	keys := stateKeys(t, home, "t1")

	if keys.List != nil || keys.Agent != nil || keys.State != "review" {
		t.Fatalf("state.json = %+v, want the state review and no list", keys)
	}
}
