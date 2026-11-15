package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildSfy builds the command with the given extra go build flags and returns the binary path.
func buildSfy(t *testing.T, flags ...string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "sfy")
	args := append([]string{"build", "-o", bin}, flags...)
	args = append(args, ".")
	if out, err := exec.CommandContext(context.Background(), "go", args...).CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

func TestVersionFlag(t *testing.T) {
	cases := []struct {
		name  string
		flags []string
		want  string
	}{
		{"plain build prints dev", nil, "dev"},
		{"ldflags set the version", []string{"-ldflags", "-X main.version=1.2.3"}, "1.2.3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := exec.CommandContext(context.Background(), buildSfy(t, tc.flags...), "--version").Output()
			if err != nil {
				t.Fatalf("sfy --version: %v", err)
			}
			if got := strings.TrimSpace(string(out)); got != tc.want {
				t.Errorf("sfy --version = %q, want %q", got, tc.want)
			}
		})
	}
}
