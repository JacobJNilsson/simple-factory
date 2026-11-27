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

func runs(t *testing.T, r Record) bool {
	t.Helper()
	ok, err := Runs(r)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func record(t *testing.T, pid int) Record {
	t.Helper()
	s, ok, err := start(pid)
	if err != nil || !ok {
		t.Fatalf("start of %d: %v, found %v", pid, err, ok)
	}
	return Record{Pid: pid, Start: s}
}

func TestRunsIsTrueForTheCallingProcess(t *testing.T) {
	if !runs(t, record(t, os.Getpid())) {
		t.Error("the test process does not run the caller")
	}
}

func TestRunsIsTrueForTheParentOfTheCaller(t *testing.T) {
	if !runs(t, record(t, os.Getppid())) {
		t.Error("the parent of the test process does not run the caller")
	}
}

func TestRunsIsFalseForAReusedPid(t *testing.T) {
	if runs(t, Record{Pid: os.Getpid(), Start: "not this process"}) {
		t.Error("a pid with another start time runs the caller")
	}
}

func TestRunsIsFalseForASibling(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	if runs(t, record(t, cmd.Process.Pid)) {
		t.Error("a child of the test process runs the caller")
	}
}

func TestReadIsFalseWithoutAFile(t *testing.T) {
	r, ok, err := Read(filepath.Join(t.TempDir(), "process"))
	if err != nil || ok {
		t.Errorf("Read of a missing file: %+v, %v, %v", r, ok, err)
	}
}

func TestReadReturnsTheWrittenRecord(t *testing.T) {
	want := Record{Pid: 42, Start: "any"}

	got, ok, err := Read(writeRecord(t, want))
	if err != nil || !ok || got != want {
		t.Errorf("Read: %+v, %v, %v, want %+v", got, ok, err, want)
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
