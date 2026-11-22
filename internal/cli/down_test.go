package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestDownWithoutASfyProcessPrintsNotRunningAndSucceeds(t *testing.T) {
	home, err := os.MkdirTemp("", "fc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	var out, errb bytes.Buffer

	code := Run([]string{"down"}, envOf(map[string]string{"SFY_HOME": home}), strings.NewReader(""), &out, &errb)

	if code != exitOK || out.String() != "sfy is not running.\n" || errb.Len() != 0 {
		t.Fatalf("code %d, stdout %q, stderr %q", code, out.String(), errb.String())
	}
}

func TestDownPrintsWhatItStopped(t *testing.T) {
	env := liveServer(t)
	var out, errb bytes.Buffer

	code := Run([]string{"down"}, env, strings.NewReader(""), &out, &errb)

	if code != exitOK || out.String() != "Stopped lead and the sfy process.\n" {
		t.Fatalf("code %d, stdout %q, stderr %q", code, out.String(), errb.String())
	}
}

func TestDownByAnAgentFails(t *testing.T) {
	home := liveServer(t)
	env := func(k string) string {
		if k == "SFY_AGENT" {
			return "lead"
		}
		return home(k)
	}
	var out, errb bytes.Buffer

	code := Run([]string{"down"}, env, strings.NewReader(""), &out, &errb)

	if code != exitFailed || !strings.Contains(errb.String(), "not for") {
		t.Fatalf("code %d, stderr %q", code, errb.String())
	}
}

func TestDownTakesNoArguments(t *testing.T) {
	var out, errb bytes.Buffer

	code := Run([]string{"down", "now"}, envOf(nil), strings.NewReader(""), &out, &errb)

	if code != exitUsage {
		t.Fatalf("code %d", code)
	}
}

func TestUsageListsDown(t *testing.T) {
	var b bytes.Buffer
	PrintUsage(&b)
	if !strings.Contains(b.String(), "sfy down") {
		t.Fatal("usage does not list sfy down")
	}
}
