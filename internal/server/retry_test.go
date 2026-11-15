package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/engine"
)

func TestRetryableAcceptErrors(t *testing.T) {
	for _, err := range []error{syscall.EMFILE, syscall.ENFILE, syscall.ECONNABORTED, fmt.Errorf("accept: %w", syscall.EMFILE)} {
		if !retryable(err) {
			t.Errorf("%v must be retryable", err)
		}
	}
	if retryable(errors.New("closed")) {
		t.Error("an unknown error must end Serve")
	}
}

// failingListener makes Accept return a fatal error.
type failingListener struct {
	net.Listener
	err error
}

func (f failingListener) Accept() (net.Conn, error) { return nil, f.err }

func TestFatalAcceptErrorEndsServe(t *testing.T) {
	dir, err := os.MkdirTemp("", "fs")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	socket := filepath.Join(dir, "f.sock")
	boom := errors.New("boom")
	eng := engine.New(nil, nil, nil, config.Config{}, nil)
	wrap := func(ln net.Listener) net.Listener { return failingListener{ln, boom} }

	done := make(chan error, 1)
	go func() { done <- serve(context.Background(), socket, eng, time.Hour, io.Discard, wrap) }()
	select {
	case err = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Serve hangs after a fatal Accept error")
	}
	if !errors.Is(err, boom) {
		t.Fatalf("want the Accept error, got %v", err)
	}
	if _, err := os.Lstat(socket); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket file remains: %v", err)
	}
	lock, err := takeLock(filepath.Join(dir, "sfy.lock"))
	if err != nil {
		t.Fatalf("lock still held: %v", err)
	}
	_ = lock.Close()
}
