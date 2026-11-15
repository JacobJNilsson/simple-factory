package server

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPingGivesUpOnAServerThatNeverAnswers(t *testing.T) {
	dir, err := os.MkdirTemp("", "p")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "unix", filepath.Join(dir, "s"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			// The connection stays open and unanswered until the process ends.
			_ = c
		}
	}()
	start := time.Now()
	if err := Ping(ln.Addr().String(), 100*time.Millisecond); err == nil {
		t.Fatal("want an error from a server that never answers")
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("Ping took %s", took)
	}
}
