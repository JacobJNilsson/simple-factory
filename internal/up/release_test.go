package up

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tarball returns a gzip tar archive that holds one file.
func tarball(t *testing.T, name, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// fakeTag is the tag that fakeRelease publishes as the latest release.
const fakeTag = "v0.2.0"

// fakeRelease serves the latest-release redirect, the archive and the checksums.
// sum overrides the checksum of the archive when it is not empty.
func fakeRelease(t *testing.T, binary, sum string) *httptest.Server {
	t.Helper()
	tag := fakeTag
	version := strings.TrimPrefix(tag, "v")
	name := fmt.Sprintf("sfy_%s_linux_amd64.tar.gz", version)
	archive := tarball(t, "sfy", binary)
	if sum == "" {
		h := sha256.Sum256(archive)
		sum = hex.EncodeToString(h[:])
	}
	dir := "/" + releaseRepo + "/releases/"
	mux := http.NewServeMux()
	mux.HandleFunc(dir+"latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dir+"tag/"+tag, http.StatusFound)
	})
	mux.HandleFunc(dir+"download/"+tag+"/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, "%s  %s\n%s  other.tar.gz\n", sum, name, strings.Repeat("0", 64))
	})
	mux.HandleFunc(dir+"download/"+tag+"/"+name, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// releaseFor returns a release that talks to srv and replaces a fake binary in a new folder.
func releaseFor(t *testing.T, srv *httptest.Server, current string) (release, *strings.Builder) {
	t.Helper()
	target := filepath.Join(t.TempDir(), "sfy")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil { //nolint:gosec // A test binary must be executable.
		t.Fatal(err)
	}
	var out strings.Builder
	return release{base: srv.URL, goos: "linux", goarch: "amd64", current: current, target: target, client: srv.Client(), out: &out}, &out
}

func content(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestReleaseUpdateSaysAlreadyNewest(t *testing.T) {
	r, out := releaseFor(t, fakeRelease(t, "new", ""), "0.2.0")
	if err := r.update(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := "sfy 0.2.0 is already the newest version.\n"; out.String() != want {
		t.Fatalf("out = %q, want %q", out.String(), want)
	}
	if got := content(t, r.target); got != "old" {
		t.Fatalf("binary = %q, want it unchanged", got)
	}
}

func TestReleaseUpdateInstallsTheNewestRelease(t *testing.T) {
	r, out := releaseFor(t, fakeRelease(t, "new binary", ""), "0.1.0")
	if err := r.update(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := "Upgraded sfy from 0.1.0 to 0.2.0.\n"; out.String() != want {
		t.Fatalf("out = %q, want %q", out.String(), want)
	}
	if got := content(t, r.target); got != "new binary" {
		t.Fatalf("binary = %q", got)
	}
	info, err := os.Stat(r.target)
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v, err = %v", info, err)
	}
	entries, err := os.ReadDir(filepath.Dir(r.target))
	if err != nil || len(entries) != 1 {
		t.Fatalf("folder holds %v, want only the binary (%v)", entries, err)
	}
}

func TestReleaseUpdateKeepsTheOldBinaryOnAChecksumMismatch(t *testing.T) {
	r, out := releaseFor(t, fakeRelease(t, "new binary", strings.Repeat("a", 64)), "0.1.0")
	err := r.update(context.Background())
	if !errors.Is(err, errChecksumMismatch) {
		t.Fatalf("err = %v", err)
	}
	if got := content(t, r.target); got != "old" {
		t.Fatalf("binary = %q, want the old one", got)
	}
	if out.Len() != 0 {
		t.Fatalf("out = %q", out.String())
	}
}

func TestReleaseUpdateKeepsTheOldBinaryWhenTheServerFails(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	r, _ := releaseFor(t, srv, "0.1.0")
	if err := r.update(context.Background()); err == nil {
		t.Fatal("want an error")
	}
	if got := content(t, r.target); got != "old" {
		t.Fatalf("binary = %q, want the old one", got)
	}
}

func TestReleaseUpdateNamesTheOperatorCommandWhenTheFolderIsNotWritable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write every folder")
	}
	r, out := releaseFor(t, fakeRelease(t, "new binary", ""), "0.1.0")
	dir := filepath.Dir(r.target)
	if err := os.Chmod(dir, 0o500); err != nil { //nolint:gosec // The test needs a folder that it cannot write.
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) //nolint:gosec // The cleanup lets the test folder go.
	err := r.update(context.Background())
	var nw *notWritableError
	if !errors.As(err, &nw) {
		t.Fatalf("err = %v", err)
	}
	want := "sudo install -m 0755 " + nw.staged + " " + r.target
	if got := nw.installCommand(); got != want {
		t.Fatalf("install command = %q, want %q", got, want)
	}
	if got := content(t, nw.staged); got != "new binary" {
		t.Fatalf("staged binary = %q", got)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(nw.staged)) })
	if got := content(t, r.target); got != "old" {
		t.Fatalf("binary = %q, want the old one", got)
	}
	if out.Len() != 0 {
		t.Fatalf("out = %q", out.String())
	}
}

func TestBrewManagedDetectsABrewPath(t *testing.T) {
	root := t.TempDir()
	cask := filepath.Join(root, "Caskroom", "sfy", "0.1.0")
	if err := os.MkdirAll(cask, 0o700); err != nil {
		t.Fatal(err)
	}
	caskBinary := filepath.Join(cask, "sfy")
	if err := os.WriteFile(caskBinary, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "bin-sfy")
	if err := os.Symlink(caskBinary, link); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(root, "local", "bin", "sfy")
	if err := os.MkdirAll(filepath.Dir(plain), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plain, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]bool{caskBinary: true, link: true, plain: false, "/opt/homebrew/Cellar/sfy/1/bin/sfy": true, "/usr/local/bin/sfy": false} {
		if got := brewManaged(path); got != want {
			t.Errorf("brewManaged(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestUpdateAndRestartPicksTheInstallMethod(t *testing.T) {
	for _, brew := range []bool{true, false} {
		var ran []string
		f := &fakeBrew{versions: [2]string{"0.1.0", "0.1.1"}}
		u := updater{
			brewInstalled: func() bool { return brew },
			brew:          f.run,
			release: func(context.Context, io.Writer) (string, error) {
				ran = append(ran, "release")
				return "/new/sfy", nil
			},
			rerun: func(bin string, _ []string, _, _ io.Writer) error { ran = append(ran, "rerun "+bin); return nil },
		}
		var out, errOut strings.Builder
		if err := u.updateAndRestart(nil, &out, &errOut); err != nil {
			t.Fatal(err)
		}
		if brew && (!f.upgraded() || strings.Join(ran, ",") != "rerun ") {
			t.Errorf("brew install: brew calls %v, ran %v", f.calls, ran)
		}
		if !brew && (len(f.calls) != 0 || strings.Join(ran, ",") != "release,rerun /new/sfy") {
			t.Errorf("release install: brew calls %v, ran %v", f.calls, ran)
		}
	}
}

var errOffline = errors.New("offline")

func TestUpdateAndRestartKeepsTheProcessWhenTheReleaseUpdateFails(t *testing.T) {
	u := updater{
		brewInstalled: func() bool { return false },
		release:       func(context.Context, io.Writer) (string, error) { return "", errOffline },
		rerun:         func(string, []string, io.Writer, io.Writer) error { t.Fatal("the new binary ran"); return nil },
	}
	var out, errOut strings.Builder
	if err := u.updateAndRestart(nil, &out, &errOut); !errors.Is(err, errOffline) {
		t.Fatalf("err = %v", err)
	}
}

func TestRestartStartsTheSfyProcessWithTheCallersPath(t *testing.T) {
	r := newRig(t)
	r.o.environ = []string{"PATH=/home/me/.local/bin:/usr/local/go/bin:/usr/bin", "SECRET=x"}
	if err := run(r.o); err != nil {
		t.Fatal(err)
	}
	argv := r.m.starts[0]
	envFile := argv[len(argv)-4]
	got := content(t, envFile)
	if !strings.Contains(got, "PATH=/home/me/.local/bin:/usr/local/go/bin:/usr/bin") || strings.Contains(got, "SECRET") {
		t.Fatalf("env file = %q", got)
	}
}
