package cli

import (
	"bytes"
	"strings"
	"testing"
)

func printedUsage() string {
	var b bytes.Buffer
	PrintUsage(&b)
	return b.String()
}

func TestUsageNamesEveryFlag(t *testing.T) {
	text := printedUsage()
	for _, line := range []string{
		"sfy up [--headless] [--new-factory]",
		"sfy restart [--update] [--headless]",
		"sfy task new [--factory <factory>] <file|->",
		"sfy task list [--archived]",
		"sfy label [<text>]",
		"sfy factories",
	} {
		if !strings.Contains(text, line) {
			t.Errorf("the usage lacks %q", line)
		}
	}
}

// usageLine returns the usage line of the command name, which starts with "sfy" and the name.
func usageLine(text, name string) (string, bool) {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "sfy "+name || strings.HasPrefix(line, "sfy "+name+" ") {
			return line, true
		}
	}
	return "", false
}

func TestUsageListsEveryFormFlag(t *testing.T) {
	text := printedUsage()
	for name, f := range forms {
		want := f.flags
		if f.option != "" {
			want = append(want, f.option)
		}
		if len(want) == 0 {
			continue
		}
		line, ok := usageLine(text, name)
		if !ok {
			t.Errorf("the usage has no line for %q", name)
			continue
		}
		for _, flag := range want {
			if !strings.Contains(line, "["+flag) {
				t.Errorf("the usage line %q lacks [%s", line, flag)
			}
		}
	}
}

func TestUsagePointsAtTheSettingsAndTheDocs(t *testing.T) {
	text := printedUsage()
	for _, want := range []string{"headless = true", "sfy config", "docs/configuration.md", "SFY_HOME", "SFY_NAME_PREFIX"} {
		if !strings.Contains(text, want) {
			t.Errorf("the usage lacks %q", want)
		}
	}
}

func TestUsageSaysAWrapperReachesAnotherFactory(t *testing.T) {
	want := "It reaches its own factory, not the one of plain sfy."
	if !strings.Contains(printedUsage(), want) {
		t.Errorf("the usage lacks %q", want)
	}
}
