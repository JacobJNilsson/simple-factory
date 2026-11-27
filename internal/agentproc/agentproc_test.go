package agentproc

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func writeRecord(t *testing.T, r Record) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pid")
	if err := Write(path, r); err != nil {
		t.Fatal(err)
	}
	return path
}

func gone(t *testing.T, path string) bool {
	t.Helper()
	g, err := Gone(path)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestGoneIsFalseForTheRunningProcess(t *testing.T) {
	self, err := Self()
	if err != nil {
		t.Fatal(err)
	}
	if self.Pid != os.Getpid() || self.Start == "" {
		t.Fatalf("Self returned %+v", self)
	}

	if gone(t, writeRecord(t, self)) {
		t.Error("the test process counts as gone")
	}
}

func TestGoneIsTrueForAReusedPid(t *testing.T) {
	path := writeRecord(t, Record{Pid: os.Getpid(), Start: "not this process"})

	if !gone(t, path) {
		t.Error("a pid with another start time counts as the recorded process")
	}
}

func TestGoneIsTrueForAnEndedProcess(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "sleep", "0")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	path := writeRecord(t, Record{Pid: cmd.Process.Pid, Start: "any"})

	if !gone(t, path) {
		t.Error("an ended process counts as running")
	}
}

func TestGoneIsFalseWithoutARecord(t *testing.T) {
	if gone(t, filepath.Join(t.TempDir(), "pid")) {
		t.Error("a missing file counts as gone")
	}
}

func TestWriteGivesTheFileMode0600(t *testing.T) {
	path := writeRecord(t, Record{Pid: 1, Start: "any"})

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", info.Mode().Perm())
	}
}
