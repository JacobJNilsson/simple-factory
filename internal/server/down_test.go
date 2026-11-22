package server_test

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/protocol"
	"github.com/JacobJNilsson/simple-factory/internal/server"
)

func TestDownAnswersThenEndsServeAndRemovesSocket(t *testing.T) {
	eng, _, _ := newEngine(t)
	socket := newSocket(t)
	done := make(chan error, 1)
	go func() { done <- server.Serve(context.Background(), socket, eng, time.Hour, io.Discard) }()
	waitForSocket(t, socket)

	ans, err := server.Call(socket, protocol.Request{Command: "down"})

	if err != nil || !ans.OK || !strings.Contains(ans.Text, "Stopped lead") {
		t.Fatalf("answer %+v, %v", ans, err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not end after sfy down")
	}
	if _, err := os.Lstat(socket); !os.IsNotExist(err) {
		t.Fatalf("the socket stays: %v", err)
	}
}

func TestRefusedDownKeepsServing(t *testing.T) {
	eng, l, _ := newEngine(t)
	socket := newSocket(t)
	start(t, socket, eng, time.Hour, io.Discard)

	ans, err := server.Call(socket, protocol.Request{Caller: protocol.Caller{Agent: "lead"}, Command: "down"})

	if err != nil || ans.OK {
		t.Fatalf("answer %+v, %v", ans, err)
	}
	if err := server.Ping(socket, time.Second); err != nil {
		t.Fatalf("the server stopped: %v", err)
	}
	if len(l.startedNames()) != 0 {
		t.Fatal("a session started")
	}
}

func waitForSocket(t *testing.T, socket string) {
	t.Helper()
	for range 1000 {
		if c, err := dial(socket); err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("server did not start")
}
