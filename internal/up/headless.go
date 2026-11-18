package up

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/server"
)

// headlessEnv is set in the environment of a detached sfy process, so it logs to sfy.log once and not twice.
const headlessEnv = "SFY_HEADLESS"

// detached is a sfy process that runs without a terminal.
type detached struct {
	// exited closes when the process ends.
	exited <-chan struct{}
}

// spawnDetached starts argv in its own session in dir, with stdin from /dev/null and stdout and stderr appended to logPath.
func spawnDetached(dir string, env, argv []string, logPath string) (detached, error) {
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return detached{}, err
	}
	defer func() { _ = logFile.Close() }()
	null, err := os.Open(os.DevNull)
	if err != nil {
		return detached{}, err
	}
	defer func() { _ = null.Close() }()
	cmd := exec.CommandContext(context.Background(), argv[0], argv[1:]...)
	cmd.Dir, cmd.Env = dir, env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = null, logFile, logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return detached{}, err
	}
	done := make(chan struct{})
	// Wait reaps the child while this process lives. The child outlives a sfy up that exits first.
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	return detached{exited: done}, nil
}

// startHeadless starts the sfy process detached, unless one runs or starts in a window, and waits for the socket.
func (o options) startHeadless() error {
	running := func() (bool, error) { return o.mux.Alive(serveSession) }
	alive, err := o.mux.Alive(serveSession)
	if err != nil {
		return err
	}
	if _, held := server.HolderPid(o.home); !alive && !held {
		env := append(serveEnv(o.environ, o.home), headlessEnv+"=1")
		p, err := o.spawn(o.home, env, []string{o.bin, "serve"}, filepath.Join(o.home, logName))
		if err != nil {
			return err
		}
		running = func() (bool, error) {
			select {
			case <-p.exited:
				return false, nil
			default:
				return true, nil
			}
		}
	}
	return o.waitAnswering(running)
}

// waitAnswering waits until the socket answers. It fails when running reports false or the wait ends.
func (o options) waitAnswering(running func() (bool, error)) error {
	deadline := time.Now().Add(o.wait)
	for !o.answers() {
		ok, err := running()
		if err != nil {
			return err
		}
		if !ok {
			return ErrServeExited
		}
		if time.Now().After(deadline) {
			return ErrNotAnswering
		}
		time.Sleep(o.poll)
	}
	return nil
}

// stopHeadless asks a detached sfy process to end. The process removes its socket and releases its lock on SIGTERM.
func stopHeadless(home string) error {
	pid, ok := server.HolderPid(home)
	if !ok {
		return nil
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("stop the sfy process %d: %w", pid, err)
	}
	return nil
}
