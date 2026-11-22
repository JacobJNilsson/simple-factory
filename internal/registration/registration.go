// Package registration records a running agent session that registered itself as the main agent.
package registration

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Record names the session that registered and the folder it works in.
type Record struct {
	// ID is the id that the provider gives the session.
	ID  string `json:"id"`
	Dir string `json:"dir"`
}

func mainFolder(home string) string { return filepath.Join(home, "sessions", "main") }

// Path is the file that holds the record.
func Path(home string) string { return filepath.Join(mainFolder(home), "registered.json") }

// AddressPath is the file that holds the terminal address of the main agent.
// A main agent that sfy runs in the operator's terminal has one too, and it has no record.
func AddressPath(home string) string { return filepath.Join(mainFolder(home), "address") }

// Read returns the record. It reports false when the file is missing or holds no id.
func Read(home string) (Record, bool) {
	b, err := os.ReadFile(Path(home))
	if err != nil {
		return Record{}, false
	}
	var r Record
	if json.Unmarshal(b, &r) != nil || r.ID == "" {
		return Record{}, false
	}
	return r, true
}

// Write saves the record.
func Write(home string, r Record) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(mainFolder(home), 0o700); err != nil {
		return err
	}
	return os.WriteFile(Path(home), append(b, '\n'), 0o600)
}

// Folder is the registration of one factory folder.
type Folder struct{ Home string }

// Registered reports whether a session registered as the main agent.
func (f Folder) Registered() bool {
	_, ok := Read(f.Home)
	return ok
}

// Clear removes the record and the recorded address. Missing files are not an error.
func (f Folder) Clear() error {
	var errs []error
	for _, path := range []string{Path(f.Home), AddressPath(f.Home)} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
