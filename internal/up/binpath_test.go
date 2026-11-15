package up

import (
	"os"
	"path/filepath"
	"testing"
)

// brewLayout builds a Cellar binary and a bin symlink to it, like a brew install.
func brewLayout(t *testing.T) (link, cellar string) {
	t.Helper()
	dir := t.TempDir()
	cellar = filepath.Join(dir, "Cellar", "sfy", "1.0.0", "bin", "sfy")
	if err := os.MkdirAll(filepath.Dir(cellar), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cellar, []byte("x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	link = filepath.Join(dir, "bin", "sfy")
	if err := os.Symlink(cellar, link); err != nil {
		t.Fatal(err)
	}
	return link, cellar
}

func exeOf(path string) func() (string, error) {
	return func() (string, error) { return path, nil }
}

func TestBinaryKeepsTheSymlinkSfyStartedAs(t *testing.T) {
	link, cellar := brewLayout(t)
	got, err := binary(link, exeOf(cellar), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != link {
		t.Fatalf("got %s, want the symlink %s", got, link)
	}
}

func TestBinaryFindsABareNameOnPath(t *testing.T) {
	link, cellar := brewLayout(t)
	lookPath := func(name string) (string, error) {
		if name != "sfy" {
			t.Fatalf("looked up %q", name)
		}
		return link, nil
	}
	got, err := binary("sfy", exeOf(cellar), lookPath)
	if err != nil {
		t.Fatal(err)
	}
	if got != link {
		t.Fatalf("got %s, want %s", got, link)
	}
}

func TestBinaryFallsBackWhenNameIsNotOnPath(t *testing.T) {
	_, cellar := brewLayout(t)
	lookPath := func(string) (string, error) { return "", os.ErrNotExist }
	got, err := binary("sfy", exeOf(cellar), lookPath)
	if err != nil {
		t.Fatal(err)
	}
	if got != cellar {
		t.Fatalf("got %s, want %s", got, cellar)
	}
}

func TestBinaryIgnoresAPathToAnotherFile(t *testing.T) {
	_, cellar := brewLayout(t)
	other := filepath.Join(t.TempDir(), "sfy")
	if err := os.WriteFile(other, []byte("y"), 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := binary(other, exeOf(cellar), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != cellar {
		t.Fatalf("got %s, want %s", got, cellar)
	}
}

func TestBinaryMakesARelativeNameAbsolute(t *testing.T) {
	link, cellar := brewLayout(t)
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(filepath.Dir(link)); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(old) }()
	got, err := binary("./sfy", exeOf(cellar), nil)
	if err != nil {
		t.Fatal(err)
	}
	// On macOS the temp folder sits behind a symlink, so compare the files.
	if !filepath.IsAbs(got) || filepath.Base(got) != "sfy" || filepath.Base(filepath.Dir(got)) != "bin" {
		t.Fatalf("got %s, want an absolute path through bin", got)
	}
}
