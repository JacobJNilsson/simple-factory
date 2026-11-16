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
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const (
	releaseBase = "https://github.com"
	releaseRepo = "JacobJNilsson/simple-factory"
	// maxArchive and maxChecksums bound what a release download may write to memory or disk.
	maxArchive   = 200 << 20
	maxChecksums = 1 << 20
)

// tagPattern accepts the release tags that GoReleaser makes, so a redirect cannot inject path parts into a download URL.
var tagPattern = regexp.MustCompile(`^v?[0-9][0-9A-Za-z.+-]*$`)

// release downloads the newest GitHub release of sfy and installs it over the running binary.
type release struct {
	// base is the site that serves the releases, without a trailing slash.
	base string
	// goos and goarch pick the archive.
	goos, goarch string
	// current is the running version, as sfy --version prints it.
	current string
	// target is the path of the binary to replace.
	target string
	client *http.Client
	out    io.Writer
}

// systemRelease updates the running binary from GitHub.
func systemRelease(current string, out io.Writer) (release, error) {
	exe, err := os.Executable()
	if err != nil {
		return release{}, err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return release{}, err
	}
	return release{
		base: releaseBase, goos: runtime.GOOS, goarch: runtime.GOARCH,
		current: current, target: exe, client: &http.Client{Timeout: 5 * time.Minute}, out: out,
	}, nil
}

// brewManaged reports whether the binary at path lives in a Homebrew Cellar or Caskroom.
// It follows symlinks, so a link such as /opt/homebrew/bin/sfy counts. It needs no brew binary.
func brewManaged(binary string) bool {
	resolved, err := filepath.EvalSymlinks(binary)
	if err != nil {
		resolved = binary
	}
	for _, part := range strings.Split(filepath.ToSlash(resolved), "/") {
		if part == "Cellar" || part == "Caskroom" {
			return true
		}
	}
	return false
}

// notWritableError reports a target folder that the user cannot write.
// The verified binary stays in staged, so the operator can install it with the printed command.
type notWritableError struct {
	target, staged string
	err            error
}

func (e *notWritableError) Error() string {
	return fmt.Sprintf("cannot write %s: %v. sfy keeps running on the old binary. The new binary is at %s. Install it with:\n  sudo install -m 0755 %s %s",
		filepath.Dir(e.target), e.err, e.staged, e.staged, e.target)
}

func (e *notWritableError) Unwrap() error { return e.err }

// update installs the newest release when it differs from the running version, and reports what it did.
// Any failure leaves the old binary in place.
func (r release) update(ctx context.Context) error {
	tag, err := r.latestTag(ctx)
	if err != nil {
		return err
	}
	latest := strings.TrimPrefix(tag, "v")
	current := strings.TrimPrefix(r.current, "v")
	if latest == current {
		_, err = fmt.Fprintf(r.out, "sfy %s is already the newest version.\n", current)
		return err
	}
	staged, err := r.fetch(ctx, tag, latest)
	if err != nil {
		return err
	}
	if err := installFile(staged, r.target); err != nil {
		var nw *notWritableError
		if !errors.As(err, &nw) {
			_ = os.RemoveAll(filepath.Dir(staged))
		}
		return err
	}
	_ = os.RemoveAll(filepath.Dir(staged))
	_, err = fmt.Fprintf(r.out, "Upgraded sfy from %s to %s.\n", current, latest)
	return err
}

// latestTag reads the tag from the redirect of the latest-release page, without the API rate limit.
func (r release) latestTag(ctx context.Context) (string, error) {
	url := r.base + "/" + releaseRepo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return "", err
	}
	noFollow := *r.client
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := noFollow.Do(req)
	if err != nil {
		return "", fmt.Errorf("find the latest release: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	location := resp.Header.Get("Location")
	if resp.StatusCode < 300 || resp.StatusCode > 399 || location == "" {
		return "", fmt.Errorf("find the latest release: %s answered %s without a redirect", url, resp.Status)
	}
	tag := path.Base(strings.TrimRight(strings.SplitN(location, "?", 2)[0], "/"))
	if !tagPattern.MatchString(tag) {
		return "", fmt.Errorf("find the latest release: %q is not a release tag", tag)
	}
	return tag, nil
}

// fetch downloads the archive and the checksums, checks the SHA-256 sum, and extracts the binary into a new folder.
// It returns the path of the extracted binary.
func (r release) fetch(ctx context.Context, tag, version string) (string, error) {
	name := fmt.Sprintf("sfy_%s_%s_%s.tar.gz", version, r.goos, r.goarch)
	dir := r.base + "/" + releaseRepo + "/releases/download/" + tag + "/"
	sums, err := r.get(ctx, dir+"checksums.txt", maxChecksums)
	if err != nil {
		return "", err
	}
	want, err := checksumFor(string(sums), name)
	if err != nil {
		return "", err
	}
	archive, err := r.get(ctx, dir+name, maxArchive)
	if err != nil {
		return "", err
	}
	if sum := sha256.Sum256(archive); hex.EncodeToString(sum[:]) != want {
		return "", fmt.Errorf("checksum mismatch for %s, sfy keeps the old binary", name)
	}
	work, err := os.MkdirTemp("", "sfy-update-")
	if err != nil {
		return "", err
	}
	staged := filepath.Join(work, "sfy")
	if err := extractBinary(archive, staged); err != nil {
		_ = os.RemoveAll(work)
		return "", fmt.Errorf("read %s: %w", name, err)
	}
	return staged, nil
}

// get downloads url into memory and fails on any status but 200.
func (r release) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("download %s: more than %d bytes", url, limit)
	}
	return body, nil
}

// checksumFor finds the SHA-256 sum of name in the text of a checksums.txt file.
func checksumFor(text, name string) (string, error) {
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("checksums.txt has no entry for %s", name)
}

// extractBinary writes the file named sfy from a gzip tar archive to dest with mode 0755.
func extractBinary(archive []byte, dest string) error {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return errors.New("the archive holds no sfy binary")
		}
		if err != nil {
			return err
		}
		if header.Typeflag != tar.TypeReg || path.Base(header.Name) != "sfy" {
			continue
		}
		return writeFile(dest, io.LimitReader(tr, maxArchive))
	}
}

// writeFile creates path with mode 0755 and fills it from src.
func writeFile(dest string, src io.Reader) error {
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755) //nolint:gosec // The binary must be executable.
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, src); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Chmod(dest, 0o755) //nolint:gosec // The binary must be executable.
}

// installFile copies src next to target and renames the copy over target, so the replacement is atomic.
// A folder that the user cannot write gives a notWritableError.
func installFile(src, target string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	tmp, err := os.CreateTemp(filepath.Dir(target), ".sfy-new-")
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return &notWritableError{target: target, staged: src, err: err}
		}
		return err
	}
	_, copyErr := io.Copy(tmp, in)
	closeErr := tmp.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil { //nolint:gosec // The binary must be executable.
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}
