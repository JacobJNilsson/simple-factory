// Package toolfree holds the test that keeps terminal tool names out of Go code.
package toolfree

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// terminalTool matches the commands and names of terminal tools as whole words, in any case.
// The word boundary keeps unrelated words such as "regex" and "index" out.
var terminalTool = regexp.MustCompile(`(?i)\b(?:hc\s|herdr\b|rex\b)`)

func TestTerminalToolMatchesTheToolNames(t *testing.T) {
	for _, line := range []string{"run hc attach now", "the Herdr app", "uses rex new", "herdr-app do x", "HC get"} {
		if !terminalTool.MatchString(line) {
			t.Errorf("want a match in %q", line)
		}
	}
}

func TestTerminalToolIgnoresUnrelatedWords(t *testing.T) {
	for _, line := range []string{"a regex for the index", "which one", "the hcl file", "exec.Command"} {
		if terminalTool.MatchString(line) {
			t.Errorf("want no match in %q", line)
		}
	}
}

func TestNonTestGoFilesNameNoTerminalTool(t *testing.T) {
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return fs.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		return scanFile(t, path)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// scanFile reports each line of the file that names a terminal tool.
func scanFile(t *testing.T, path string) error {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		if m := terminalTool.FindString(sc.Text()); m != "" {
			t.Errorf("%s:%d names a terminal tool (%q). Terminal tool commands belong in a template set in config. See docs/herdr.md.", path, n, strings.TrimSpace(m))
		}
	}
	return sc.Err()
}
