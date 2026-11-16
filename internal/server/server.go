// Package server carries sfy commands from the CLI to the engine over a Unix socket.
package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/engine"
	"github.com/JacobJNilsson/simple-factory/internal/protocol"
)

// NotRunningError reports that nothing listens on the socket.
type NotRunningError struct {
	Socket string
}

func (e *NotRunningError) Error() string {
	return fmt.Sprintf("the sfy process does not run (no server on %s). Start it with: sfy", e.Socket)
}

// ErrAlreadyRunning reports that another sfy process holds the socket folder's lock.
var ErrAlreadyRunning = errors.New("a sfy process already runs")

// ErrNotOwner reports that the socket folder belongs to another user.
var ErrNotOwner = errors.New("the socket folder is not yours")

// maxLine limits the size of one request or answer line.
var maxLine = 16 << 20

const (
	// readTimeout limits how long a client may take to send its request line.
	readTimeout = 30 * time.Second
	// callTimeout limits a whole exchange in Call. The engine may take minutes to clone or start a session.
	callTimeout = 10 * time.Minute
	// acceptPause is the wait before Serve retries after a temporary Accept error.
	acceptPause = 50 * time.Millisecond
)

// Serve answers requests on the Unix socket until ctx ends, and calls eng.Tick every tick.
func Serve(ctx context.Context, socket string, eng *engine.Engine, tick time.Duration, log io.Writer) error {
	return serve(ctx, socket, eng, tick, log, nil)
}

// serve is Serve with a seam for tests. A non-nil wrap replaces the listener after it starts.
func serve(ctx context.Context, socket string, eng *engine.Engine, tick time.Duration, log io.Writer, wrap func(net.Listener) net.Listener) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	dir := filepath.Dir(socket)
	if err := secureDir(dir); err != nil {
		return err
	}
	lock, err := takeLock(filepath.Join(dir, "sfy.lock"))
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	if err := clearStale(socket); err != nil {
		return err
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "unix", socket)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(socket) }()
	if err := os.Chmod(socket, 0o600); err != nil {
		_ = ln.Close()
		return err
	}

	if wrap != nil {
		ln = wrap(ln)
	}
	conns := &connSet{open: map[net.Conn]bool{}}
	var wg sync.WaitGroup
	go func() {
		<-ctx.Done()
		_ = ln.Close()
		conns.closeAll()
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		tickLoop(ctx, eng, tick, log)
	}()
	// Cancel first, so the tick loop and the closer stop on every return path.
	// Shutdown also waits for a Handle that still runs, so a hung engine call delays it.
	defer func() {
		cancel()
		wg.Wait()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if retryable(err) {
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(acceptPause):
				}
				continue
			}
			return err
		}
		if !conns.add(conn) {
			_ = conn.Close()
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer conns.remove(conn)
			handle(conn, eng)
			// The answer is written, so the connection can close with the process.
			if eng.Stopping() {
				cancel()
			}
		}()
	}
}

// retryable reports whether an Accept error is temporary, such as running out of file descriptors.
func retryable(err error) bool {
	return errors.Is(err, syscall.EMFILE) || errors.Is(err, syscall.ENFILE) || errors.Is(err, syscall.ECONNABORTED)
}

// connSet tracks open connections so that shutdown can close them.
type connSet struct {
	mu     sync.Mutex
	open   map[net.Conn]bool
	closed bool
}

// add registers a connection. It returns false after closeAll, and the caller must close the connection.
func (s *connSet) add(c net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.open[c] = true
	return true
}

func (s *connSet) remove(c net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.open, c)
}

func (s *connSet) closeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for c := range s.open {
		_ = c.Close()
	}
}

// secureDir creates the socket folder, requires that the current user owns it, and narrows it to 0700.
// A 0700 folder closes the window between Listen and Chmod on the socket.
func secureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cannot read the owner of %s", dir)
	}
	if int(st.Uid) != os.Getuid() {
		return fmt.Errorf("%w: %s belongs to user %d, you are user %d. Use a folder you own", ErrNotOwner, dir, st.Uid, os.Getuid())
	}
	if info.Mode().Perm()&^0o700 != 0 {
		return os.Chmod(dir, 0o700)
	}
	return nil
}

// takeLock locks the file for as long as the returned file stays open.
func takeLock(path string) (*os.File, error) {
	f, err := os.OpenFile(filepath.Clean(path), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("%w (lock %s)", ErrAlreadyRunning, path)
		}
		return nil, err
	}
	return f, nil
}

// clearStale removes a leftover socket file. The caller holds the lock, so no server owns the file.
// Any other kind of file at the path stays, and clearStale fails.
func clearStale(socket string) error {
	info, err := os.Lstat(socket)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("%s exists and is not a socket. Remove it or choose another folder", socket)
	}
	return os.Remove(socket)
}

func tickLoop(ctx context.Context, eng *engine.Engine, tick time.Duration, log io.Writer) {
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := safeTick(eng); err != nil {
				_, _ = fmt.Fprintf(log, "%s tick: %v\n", time.Now().Format(time.RFC3339), err)
			}
		}
	}
}

// safeTick runs one tick and turns a panic into an error, so the tick loop goes on.
func safeTick(eng *engine.Engine) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return eng.Tick()
}

// safeHandle runs one command and turns a panic into an error answer, so the server goes on.
func safeHandle(eng *engine.Engine, req protocol.Request) (ans protocol.Answer) {
	defer func() {
		if r := recover(); r != nil {
			ans = protocol.Answer{Error: fmt.Sprintf("internal error: panic: %v", r)}
		}
	}()
	return eng.Handle(req)
}

var errTooLarge = errors.New("request too large")

func handle(conn net.Conn, eng *engine.Engine) {
	defer func() { _ = conn.Close() }()
	// The deadline covers the request only. The engine may take minutes to answer.
	_ = conn.SetReadDeadline(time.Now().Add(readTimeout))
	var req protocol.Request
	var ans protocol.Answer
	line, err := readLine(conn)
	if err == nil {
		err = json.Unmarshal(line, &req)
	}
	_ = conn.SetReadDeadline(time.Time{})
	switch {
	case errors.Is(err, errTooLarge):
		ans = protocol.Answer{Error: errTooLarge.Error()}
	case err != nil:
		ans = protocol.Answer{Error: fmt.Sprintf("bad request: %v", err)}
	default:
		ans = safeHandle(eng, req)
	}
	_ = json.NewEncoder(conn).Encode(ans)
	if errors.Is(err, errTooLarge) {
		// Drain the rest briefly, so that closing with unread data does not reset the answer.
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		_, _ = io.Copy(io.Discard, conn)
	}
}

// readLine reads one line of at most maxLine bytes.
func readLine(r io.Reader) ([]byte, error) {
	br := bufio.NewReaderSize(io.LimitReader(r, int64(maxLine)+1), 64<<10)
	line, err := br.ReadBytes('\n')
	if len(line) > maxLine {
		return nil, errTooLarge
	}
	return line, err
}

// Call sends one request to the server on socket and returns its answer.
func Call(socket string, req protocol.Request) (protocol.Answer, error) {
	return call(socket, req, callTimeout)
}

// Ping sends a status request and fails when no answer comes within timeout.
func Ping(socket string, timeout time.Duration) error {
	ans, err := call(socket, protocol.Request{Command: "status"}, timeout)
	if err != nil {
		return err
	}
	if !ans.OK {
		return errors.New(strings.TrimSpace(ans.Error))
	}
	return nil
}

func call(socket string, req protocol.Request, timeout time.Duration) (protocol.Answer, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socket)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
			return protocol.Answer{}, &NotRunningError{Socket: socket}
		}
		return protocol.Answer{}, err
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return protocol.Answer{}, err
	}
	line, err := readLine(conn)
	if err != nil {
		return protocol.Answer{}, fmt.Errorf("read answer: %w", err)
	}
	var ans protocol.Answer
	if err := json.Unmarshal(line, &ans); err != nil {
		return protocol.Answer{}, fmt.Errorf("bad answer: %w", err)
	}
	return ans, nil
}
