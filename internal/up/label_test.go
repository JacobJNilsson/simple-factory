package up

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/config"
)

func TestLabelWritesTheLabelFileWithMode0600(t *testing.T) {
	env, _, home := configFactory(t)

	err := Label(env, io.Discard, "Client work")

	if err != nil {
		t.Fatal(err)
	}
	if got := config.LabelOf(home); got != "Client work" {
		t.Errorf("LabelOf = %q, want %q", got, "Client work")
	}
	st, err := os.Stat(filepath.Join(home, "label"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", st.Mode().Perm())
	}
}

func TestLabelWithAnEmptyTextRemovesTheLabelFile(t *testing.T) {
	env, _, home := configFactory(t)
	writeTestFile(t, filepath.Join(home, "label"), "Client work\n")

	err := Label(env, io.Discard, "")

	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "label")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the label file stays: %v", err)
	}
	if err := Label(env, io.Discard, ""); err != nil {
		t.Errorf("a second removal fails: %v", err)
	}
}

func TestLabelRefusesATextThatIsNoValidLabel(t *testing.T) {
	cases := []struct{ name, text string }{
		{"41 characters", strings.Repeat("a", 41)},
		{"a tab", "client\twork"},
		{"a newline", "client\nwork"},
		{"invalid UTF-8", "client \xff"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, _, home := configFactory(t)
			writeTestFile(t, filepath.Join(home, "label"), "Old\n")

			err := Label(env, io.Discard, tc.text)

			var invalid *InvalidLabelError
			if !errors.As(err, &invalid) {
				t.Errorf("Label(%q) = %v, want an InvalidLabelError", tc.text, err)
			}
			if got := config.LabelOf(home); got != "Old" {
				t.Errorf("LabelOf = %q, want the old label kept", got)
			}
		})
	}
}

func TestLabelAcceptsFortyCharactersOfAnyScript(t *testing.T) {
	env, _, home := configFactory(t)
	text := strings.Repeat("ö", 40)

	err := Label(env, io.Discard, text)

	if err != nil {
		t.Fatal(err)
	}
	if got := config.LabelOf(home); got != text {
		t.Errorf("LabelOf = %q, want %q", got, text)
	}
}

func TestFactoriesListsNameLabelAndFolderAndSkipsLinks(t *testing.T) {
	userHome := t.TempDir()
	dir := config.FactoriesDir(userHome)
	writeTestFile(t, filepath.Join(dir, "web", "folder"), "/work/web\n")
	writeTestFile(t, filepath.Join(dir, "web", "label"), "Client work\n")
	writeTestFile(t, filepath.Join(dir, "api", "folder"), "/work/api\n")
	writeTestFile(t, filepath.Join(dir, "empty", "notes.md"), "no folder file\n")
	writeTestFile(t, filepath.Join(dir, "stray"), "a file\n")
	if err := os.Symlink(filepath.Join(dir, "web"), filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	env := func(k string) string { return map[string]string{"HOME": userHome}[k] }
	var out strings.Builder

	err := Factories(env, &out)

	if err != nil {
		t.Fatal(err)
	}
	want := "api\t-\t/work/api\nweb\tClient work\t/work/web\n"
	if out.String() != want {
		t.Errorf("output %q, want %q", out.String(), want)
	}
}

func TestFactoriesPrintsNothingWithoutAFactoriesFolder(t *testing.T) {
	userHome := t.TempDir()
	env := func(k string) string { return map[string]string{"HOME": userHome}[k] }
	var out strings.Builder

	err := Factories(env, &out)

	if err != nil || out.Len() != 0 {
		t.Errorf("Factories = %v with output %q, want no error and no output", err, out.String())
	}
}
