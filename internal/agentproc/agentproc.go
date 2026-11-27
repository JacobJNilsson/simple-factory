// Package agentproc records the process that runs an agent and reports whether that process still runs.
package agentproc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Record names one process. The pair of pid and start time stays unique when the system reuses a pid.
type Record struct {
	Pid int `json:"pid"`
	// Start is the start time of the process as ps prints it.
	Start string `json:"start"`
}

// Self returns the record of the calling process. An exec keeps both fields, so the record names the replacement too.
func Self() (Record, error) {
	pid := os.Getpid()
	s, ok, err := start(pid)
	if err != nil {
		return Record{}, err
	}
	if !ok {
		return Record{}, fmt.Errorf("ps finds no process %d", pid)
	}
	return Record{Pid: pid, Start: s}, nil
}

// Write saves r as JSON at path with mode 0600.
func Write(path string, r Record) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

// Gone reports true only when path holds a record whose process no longer runs, or runs with another start time.
// A missing or malformed file gives false and no error, because then nothing names a process.
func Gone(path string) (bool, error) {
	r, found, err := Read(path)
	if err != nil || !found {
		return false, err
	}
	s, ok, err := start(r.Pid)
	if err != nil {
		return false, err
	}
	return !ok || s != r.Start, nil
}

// Read returns the record at path. A missing or malformed file gives false and no error.
func Read(path string) (Record, bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, err
	}
	var r Record
	if json.Unmarshal(b, &r) != nil || r.Pid <= 0 {
		return Record{}, false, nil
	}
	return r, true, nil
}

// maxDepth limits the walk up the parent chain, so a loop in the process table cannot stall Runs.
const maxDepth = 64

// Runs reports whether r names the calling process or one of its ancestors, with the same start time.
// A failed ps is an error and never a yes.
func Runs(r Record) (bool, error) {
	if r.Pid <= 0 {
		return false, nil
	}
	parents, err := parentTable()
	if err != nil {
		return false, err
	}
	pid := os.Getpid()
	for range maxDepth {
		if pid == r.Pid {
			s, ok, err := start(pid)
			if err != nil {
				return false, err
			}
			return ok && s == r.Start, nil
		}
		next, ok := parents[pid]
		if !ok || pid <= 1 || next == pid {
			return false, nil
		}
		pid = next
	}
	return false, nil
}

// parentTable maps the pid of each running process to the pid of its parent.
func parentTable() (map[int]int, error) {
	cmd := exec.CommandContext(context.Background(), "ps", "-A", "-o", "pid=", "-o", "ppid=")
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ps -A -o pid= -o ppid=: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	parents := map[int]int{}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		pid, perr := strconv.Atoi(fields[0])
		ppid, qerr := strconv.Atoi(fields[1])
		if perr == nil && qerr == nil {
			parents[pid] = ppid
		}
	}
	return parents, nil
}

// start returns the start time of process pid. It reports false when no such process runs.
func start(pid int) (string, bool, error) {
	cmd := exec.CommandContext(context.Background(), "ps", "-o", "lstart=", "-p", strconv.Itoa(pid))
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	s := strings.TrimSpace(string(out))
	var exit *exec.ExitError
	// ps exits 1 with no output when it finds no process with the pid.
	if errors.As(err, &exit) && exit.ExitCode() == 1 && s == "" && stderr.Len() == 0 {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("ps -o lstart= -p %d: %w: %s", pid, err, strings.TrimSpace(stderr.String()))
	}
	if s == "" {
		return "", false, nil
	}
	return s, true, nil
}
