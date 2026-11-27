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
		"sfy up [--headless]",
		"sfy restart [--update]",
		"sfy task new [--factory <factory>] <file|->",
		"sfy task list [--archived]",
	} {
		if !strings.Contains(text, line) {
			t.Errorf("the usage lacks %q", line)
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
