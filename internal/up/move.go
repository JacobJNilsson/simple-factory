package up

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/JacobJNilsson/simple-factory/internal/protocol"
	"github.com/JacobJNilsson/simple-factory/internal/registration"
)

// oldEntries lists the entries of a factory folder that named the lead by its old name, and their new names.
// The paths are relative to the factory folder.
var oldEntries = []struct{ old, new string }{
	{old: filepath.Join("sessions", protocol.OldLead), new: filepath.Join("sessions", protocol.Lead)},
	{old: filepath.Join("inbox", protocol.OldLead), new: filepath.Join("inbox", protocol.Lead)},
	{old: protocol.OldLead + "-prompt.md", new: protocol.Lead + "-prompt.md"},
}

// moveError reports that sfy cannot tell whether the lead runs in the terminal with the old name, so it moved nothing.
// A guess could start a second lead.
type moveError struct {
	Terminal string
	Err      error
}

func (e *moveError) Error() string {
	return fmt.Sprintf("sfy cannot tell whether the lead runs as %s, so it moves nothing: %v", e.Terminal, e.Err)
}

func (e *moveError) Unwrap() error { return e.Err }

// moveToLead moves a factory folder from the old name of the lead to the new one, so a lead that runs keeps its session.
// It renames each old entry whose new name is absent, and leaves a link with the old name, so an older sfy and outside readers still find the files.
// Before it moves the session folder, it asks alive whether the terminal prefix+main runs.
// When it does, it records that terminal in the address file, so sfy reaches the running lead and starts no second one.
// A failed alive stops the move before it changes anything. A second call finds nothing to move.
func moveToLead(home, prefix string, alive func(name string) (bool, error)) error {
	pending, err := sessionMovePending(home)
	if err != nil {
		return err
	}
	running := false
	if pending {
		if running, err = alive(prefix + protocol.OldLead); err != nil {
			return &moveError{Terminal: prefix + protocol.OldLead, Err: err}
		}
	}
	for _, e := range oldEntries {
		if err := moveEntry(filepath.Join(home, e.old), filepath.Join(home, e.new)); err != nil {
			return err
		}
	}
	if !running {
		return nil
	}
	path := registration.AddressPath(home)
	if _, err := os.Stat(path); err == nil || !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return os.WriteFile(path, []byte(prefix+protocol.OldLead+"\n"), 0o600)
}

// sessionMovePending reports whether the session folder of the lead still has its old name and no folder has the new one.
func sessionMovePending(home string) (bool, error) {
	old, err := os.Lstat(filepath.Join(home, oldEntries[0].old))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !old.IsDir() {
		return false, nil
	}
	_, err = os.Lstat(filepath.Join(home, oldEntries[0].new))
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	return false, err
}

// moveEntry renames from to to and leaves a relative link at from, when from is no link and to is absent. Otherwise it changes nothing.
func moveEntry(from, to string) error {
	info, err := os.Lstat(from)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil
	}
	if _, err := os.Lstat(to); err == nil || !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.Rename(from, to); err != nil {
		return err
	}
	return os.Symlink(filepath.Base(to), from)
}
