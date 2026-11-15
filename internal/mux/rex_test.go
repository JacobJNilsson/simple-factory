package mux

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRex writes a rex script with the given body into a new folder and returns the folder.
func fakeRex(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "rex"), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// onlyPath makes the fake rex folder and the system tools the whole PATH, with no real rex.
func onlyPath(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("PATH", dir+":/usr/bin:/bin")
	t.Setenv("REX_BIN_DIR", "")
}

func TestRexSetHasNoShow(t *testing.T) {
	set, ok := Builtin("rex")
	if !ok || set.Show != "" || set.Start == "" || set.Send == "" || set.Alive == "" || set.Stop == "" {
		t.Fatalf("got %+v, %v", set, ok)
	}
}

func TestRexStartAndSendKeepQuotes(t *testing.T) {
	set, _ := Builtin("rex")
	log := filepath.Join(t.TempDir(), "log")
	dir := fakeRex(t, `case $1 in
block) echo 'session target "x" not found' >&2; exit 3;;
*) for a in "$@"; do printf '[%s]' "$a" >> `+shellQuote(log)+`; done; echo >> `+shellQuote(log)+`;;
esac`)
	onlyPath(t, dir)
	m := Runner(set)
	if err := m.Start("w 1", "/tmp/d d", []string{"claude", "it's", `a "b" åäö`}); err != nil {
		t.Fatal(err)
	}
	text := `-x it's "q" åäö`
	if err := m.Send("w 1", text); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(log)
	want := "[new][w 1][-c][/tmp/d d][--keep-open][--][claude][it's][a \"b\" åäö]\n" +
		"[send][-s][w 1][--][" + text + "]\n[send-key][-s][w 1][enter]\n"
	if string(got) != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestRexStartRefusesAnExistingLabel(t *testing.T) {
	set, _ := Builtin("rex")
	cases := map[string]string{
		"exists":    `echo '{"child":{"pid":1}}'`,
		"ambiguous": `echo 'session target "x" is ambiguous' >&2; exit 3`,
		"down":      `echo 'server down' >&2; exit 1`,
	}
	for name, block := range cases {
		t.Run(name, func(t *testing.T) {
			log := filepath.Join(t.TempDir(), "log")
			onlyPath(t, fakeRex(t, `case $1 in block) `+block+`;; new) echo new >> `+shellQuote(log)+`;; esac`))
			if err := Runner(set).Start("x", "/", []string{"true"}); err == nil {
				t.Fatal("want an error")
			}
			if _, err := os.Stat(log); err == nil {
				t.Fatal("rex new ran")
			}
		})
	}
}

func TestRexAliveCases(t *testing.T) {
	set, _ := Builtin("rex")
	cases := []struct {
		name, body string
		want       bool
		wantErr    bool
	}{
		{"running", `echo '{"child": {"pid": 4}, "last_exit": null}'`, true, false},
		{"finished", `echo '{"child": null, "last_exit": {"exit_code": 0}}'`, false, false},
		{"finished compact", `echo '{"child":null}'`, false, false},
		{"missing", `echo 'session target "x" not found' >&2; exit 3`, false, false},
		{"ambiguous", `echo 'session target "x" is ambiguous' >&2; exit 3`, false, true},
		{"server down", `echo 'dial: refused' >&2; exit 1`, false, true},
		{"unknown output", `echo '{}'`, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			onlyPath(t, fakeRex(t, "[ \"$1\" = --autostart=false ] || exit 9\n"+c.body))
			got, err := Runner(set).Alive("x")
			if got != c.want || (err != nil) != c.wantErr {
				t.Fatalf("got %v, %v", got, err)
			}
		})
	}
}

func TestRexStopKillsTheLabel(t *testing.T) {
	set, _ := Builtin("rex")
	log := filepath.Join(t.TempDir(), "log")
	onlyPath(t, fakeRex(t, `echo "$@" > `+shellQuote(log)))
	if err := Runner(set).Stop("w 1"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(log)
	if strings.TrimSpace(string(got)) != "--autostart=false kill w 1" {
		t.Fatalf("got %q", got)
	}
}

func TestRexBinaryLookup(t *testing.T) {
	set, _ := Builtin("rex")
	out := filepath.Join(t.TempDir(), "out")
	t.Setenv("OUT", out)
	stub := func(dir, tag string) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "rex"), []byte("#!/bin/sh\necho "+tag+" > \"$OUT\"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	read := func() string { b, _ := os.ReadFile(out); return strings.TrimSpace(string(b)) }
	pathDir, binDir, home := t.TempDir(), t.TempDir(), t.TempDir()
	stub(pathDir, "path")
	stub(binDir, "bindir")
	stub(filepath.Join(home, "Library/Application Support/com.superlogical.rex/RexServer/Versions/Current/Rex Server.app/Contents/MacOS"), "app")
	t.Setenv("HOME", home)
	t.Setenv("PATH", pathDir+":/usr/bin:/bin")
	t.Setenv("REX_BIN_DIR", binDir)
	if err := Runner(set).Stop("x"); err != nil || read() != "path" {
		t.Fatalf("PATH first: %q, %v", read(), err)
	}
	t.Setenv("PATH", "/usr/bin:/bin")
	if err := Runner(set).Stop("x"); err != nil || read() != "bindir" {
		t.Fatalf("REX_BIN_DIR second: %q, %v", read(), err)
	}
	t.Setenv("REX_BIN_DIR", "")
	if err := Runner(set).Stop("x"); err != nil || read() != "app" {
		t.Fatalf("app path last: %q, %v", read(), err)
	}
}
