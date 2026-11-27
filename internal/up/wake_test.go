package up

import (
	"os"
	"reflect"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/engine"
)

func TestTheWakeSpecIsTheLeadSpecWithTheWakePrompt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	home := t.TempDir()
	o := options{home: home, dir: "/work", config: config.Config{Models: config.Models{Lead: "opus"}}}

	got, err := o.wakeSpec()()

	if err != nil {
		t.Fatal(err)
	}
	want := engine.SessionSpec{
		Name: "lead", Kind: "lead", Dir: "/work", Model: "opus", Prompt: leadPrompt(home), ResumePrompt: leadWakePrompt(home),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if _, err := os.Stat(leadPromptPath(home)); err != nil {
		t.Errorf("the wake spec did not record the lead prompt: %v", err)
	}
}
