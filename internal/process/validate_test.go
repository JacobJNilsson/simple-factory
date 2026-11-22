package process_test

import (
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/process"
)

// ingestion returns the ingestion process of the factory plan, merged onto the default.
func ingestion(t *testing.T) process.Process {
	t.Helper()
	text, err := os.ReadFile("testdata/ingestion.toml")
	if err != nil {
		t.Fatal(err)
	}
	return load(t, string(text))
}

func TestTheIngestionProcessValidates(t *testing.T) {
	p := ingestion(t)
	if err := p.Validate(nil); err != nil {
		t.Fatal(err)
	}
	got, err := p.Flatten("production")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"release", "publish", "promote", "verify"}; !slices.Equal(got, want) {
		t.Fatalf("Flatten(production) = %v, want %v", got, want)
	}
	if p.Start != "load" || p.Owner("publish").Name != "loader" {
		t.Fatalf("start %q and owner of publish %q, want load and loader", p.Start, p.Owner("publish").Name)
	}
}

func TestValidateRefusesAReservedName(t *testing.T) {
	cases := map[string]struct{ text, key string }{
		"an agent named lead":                    {"[agents.lead]\nharness = \"shell\"\nrun = [\"true\"]", "agents.lead"},
		"a list named main":                      {"[lists.main]\nagents = [\"worker\"]", "lists.main"},
		"a session prefix outside the name rule": {"[agents.drafter]\nharness = \"claude\"\nsession_name = \"Draft\"", "agents.drafter.session_name"},
		"a session_name on a like agent":         {"[agents.push]\nsession_name = \"pusher\"", "agents.push.session_name"},
	}
	for name, c := range cases {
		err := load(t, c.text).Validate(nil)
		var keyErr *process.KeyError
		if !errors.As(err, &keyErr) || keyErr.Key != c.key {
			t.Errorf("%s: Validate = %v, want an error that names %s", name, err, c.key)
		}
	}
}

func TestValidateRefusesASessionPrefixThatTwoAgentsShare(t *testing.T) {
	err := load(t, "[agents.drafter]\nharness = \"claude\"\nsession_name = \"worker\"").Validate(nil)
	var shared *process.PrefixError
	if !errors.As(err, &shared) || shared.Prefix != "worker" || !slices.Equal(shared.Agents, []string{"drafter", "worker"}) {
		t.Fatalf("Validate = %v, want an error that names the prefix worker of drafter and worker", err)
	}
}
