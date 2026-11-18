package engine

import "time"

// CheckResult is the result of one run of a task's check command.
type CheckResult struct {
	// Output is the kept tail of the command's stdout and stderr.
	Output string
	// Cut is the number of bytes that sfy dropped from the start of the output.
	Cut int
	// Err is nil when the command exited with 0. Otherwise it holds the exit error, the start error or the timeout.
	Err error
	// TimedOut is true when the timeout stopped the command.
	TimedOut bool
	Took     time.Duration
}

// Checker runs the check command of a task.
type Checker interface {
	// Start runs command in dir on another goroutine and calls done once with the result.
	// It returns at once and never calls done before it returns.
	Start(dir, command string, done func(CheckResult))
}
