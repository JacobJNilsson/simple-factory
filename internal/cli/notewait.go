package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/provider/claude"
)

const (
	// noteWaitPoll is how often the waiter looks at the note file. A note reaches an idle session within about this time.
	noteWaitPoll = time.Second
	// noteWaitMax is how long one waiter runs. The hook timeout in the session settings is longer, and the next turn arms a new waiter.
	noteWaitMax = 12 * time.Hour
	// lockRetries is how many polls a new waiter waits for the waiter of an earlier session to let go of the lock.
	lockRetries = 5
)

// noteWait is the background hook that wakes an idle session. It waits until the session's note file holds text,
// takes the text as the note hook does, and exits with exitWake so Claude Code starts a turn with it.
// One waiter runs for each session. A second one exits at once. The waiter also exits when its parent process ends.
func noteWait(env func(string) string, stderr io.Writer, poll, limit time.Duration) int {
	path, ok := claude.NotePath(config.Home(env), env("SFY_AGENT"))
	if !ok {
		return exitOK
	}
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		return exitOK
	}
	var lock *os.File
	for i := 0; i < lockRetries && lock == nil; i++ {
		var err error
		if lock, err = claude.LockWaiter(path); err != nil {
			_, _ = fmt.Fprintf(stderr, "sfy: %v\n", err)
			return exitFailed
		}
		if lock == nil {
			time.Sleep(poll)
		}
	}
	if lock == nil {
		return exitOK
	}
	defer func() { _ = lock.Close() }()
	parent := os.Getppid()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if os.Getppid() != parent {
			return exitOK
		}
		if claude.NotePending(path) {
			code, err := takeNotes(path, env, stderr)
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "sfy: %v\n", err)
			}
			if code == exitWake {
				return exitWake
			}
		}
		time.Sleep(poll)
	}
	return exitOK
}
