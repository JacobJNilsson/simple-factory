// Package envruntest parses the commands that envrun.Wrap returns, for tests.
package envruntest

import (
	"os"
	"slices"
	"strings"
	"testing"
)

// Command is a parsed `<bin> run [-i] <file> -- <cmd...>` argv.
type Command struct {
	Bin   string
	Clean bool
	File  string
	Cmd   []string
	// Env holds the entries of the env file.
	Env []string
}

// Parse splits argv and reads its env file. It fails the test when argv is not a sfy run command.
func Parse(t *testing.T, argv []string) Command {
	t.Helper()
	sep := slices.Index(argv, "--")
	if len(argv) < 5 || argv[1] != "run" || sep < 3 || sep > 4 || sep == len(argv)-1 {
		t.Fatalf("not a sfy run command: %v", argv)
	}
	c := Command{Bin: argv[0], Clean: sep == 4, File: argv[sep-1], Cmd: argv[sep+1:]}
	if c.Clean && argv[2] != "-i" {
		t.Fatalf("unknown flag in %v", argv)
	}
	b, err := os.ReadFile(c.File)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) > 0 {
		c.Env = strings.Split(string(b), "\x00")
	}
	return c
}
