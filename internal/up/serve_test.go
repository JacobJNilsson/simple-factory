package up

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// longHome returns a home whose socket path is over every platform limit.
func longHome(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), strings.Repeat("a", 120))
}

func TestServeWritesAFatalStartupErrorToTheLog(t *testing.T) {
	home := longHome(t)
	env := func(k string) string {
		if k == "SFY_HOME" {
			return home
		}
		return ""
	}

	err := Serve(context.Background(), env, &bytes.Buffer{})

	var perr *socketPathError
	if !errors.As(err, &perr) {
		t.Fatalf("got %v", err)
	}
	log, rerr := os.ReadFile(filepath.Join(home, logName))
	if rerr != nil || len(log) == 0 {
		t.Fatalf("log %q, %v", log, rerr)
	}
}

func TestUpRefusesALongSocketPathBeforeItStartsAnything(t *testing.T) {
	home := longHome(t)
	env := func(k string) string {
		if k == "SFY_HOME" {
			return home
		}
		return ""
	}

	err := Command(nil, env, &bytes.Buffer{}, io.Discard, false)

	var perr *socketPathError
	if !errors.As(err, &perr) || perr.max != socketPathLimit()-1 {
		t.Fatalf("got %v", err)
	}
	if _, serr := os.Stat(home); serr == nil {
		t.Fatal("up created the home before it checked the path")
	}
}

func TestCheckSocketPathAllowsOneByteUnderTheLimit(t *testing.T) {
	limit := socketPathLimit()

	if err := checkSocketPath(strings.Repeat("a", limit-1)); err != nil {
		t.Fatal(err)
	}
	if err := checkSocketPath(strings.Repeat("a", limit)); err == nil {
		t.Fatal("a path at the limit must fail")
	}
}
