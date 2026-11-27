package mux

import "testing"

func whoamiRunner(tpl string) runner {
	return runner{t: Templates{Whoami: tpl}, timeout: commandTimeout}
}

func TestFromMapAcceptsOptionalWhoami(t *testing.T) {
	got, err := FromMap(map[string]string{"start": "a", "send": "b", "alive": "c", "stop": "d", "whoami": "e"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Whoami != "e" {
		t.Fatalf("got %q, want e", got.Whoami)
	}
}

func TestWhoamiReturnsTheTrimmedAddress(t *testing.T) {
	got, err := whoamiRunner(`printf '  dev-1\n'`).Whoami()
	if err != nil || got != "dev-1" {
		t.Fatalf("got %q, %v, want dev-1", got, err)
	}
}

func TestWhoamiWithoutATemplateReturnsNothing(t *testing.T) {
	got, err := whoamiRunner("").Whoami()
	if err != nil || got != "" {
		t.Fatalf("got %q, %v, want an empty address", got, err)
	}
}

func TestWhoamiFailureIsAnError(t *testing.T) {
	if _, err := whoamiRunner(`echo no tool >&2; exit 3`).Whoami(); err == nil {
		t.Fatal("want an error")
	}
}

func TestWhoamiWithSeveralLinesIsAnError(t *testing.T) {
	if _, err := whoamiRunner(`printf 'a\nb\n'`).Whoami(); err == nil {
		t.Fatal("want an error")
	}
}

func TestWhoamiSeesTheEnvironmentOfSfy(t *testing.T) {
	t.Setenv("SFY_TEST_PANE", "%7")
	got, err := whoamiRunner(`printf '%s' "$SFY_TEST_PANE"`).Whoami()
	if err != nil || got != "%7" {
		t.Fatalf("got %q, %v, want %%7", got, err)
	}
}

func TestPrefixedPassesWhoamiOnWithoutThePrefix(t *testing.T) {
	m, ok := Prefixed(Registered(whoamiRunner(`printf dev`), "pre-lead", "/none", nil), "pre-").(Whoamier)
	if !ok {
		t.Fatal("Prefixed must pass Whoami on")
	}
	got, err := m.Whoami()
	if err != nil || got != "dev" {
		t.Fatalf("got %q, %v, want dev", got, err)
	}
}

func TestTmuxSetHasAWhoamiTemplate(t *testing.T) {
	set, _ := Builtin("tmux")
	if set.Whoami == "" {
		t.Fatal("the tmux set needs a whoami template")
	}
}
