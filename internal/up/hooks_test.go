package up

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallHooksPrintsWhatItChangedThenNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude", "settings.json")
	var out bytes.Buffer

	if err := installHooks(path, "/bin/sfy", &out); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), "Changed "+path) || !strings.Contains(out.String(), "- Added the Stop hook: /bin/sfy hook --user turn-ended") {
		t.Errorf("first output %q", out.String())
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	out.Reset()

	if err := installHooks(path, "/bin/sfy", &out); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), "Nothing changed") {
		t.Errorf("second output %q", out.String())
	}
}
