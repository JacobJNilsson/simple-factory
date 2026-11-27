// Package registration records a running agent session that registered itself as the lead.
package registration

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/JacobJNilsson/simple-factory/internal/protocol"
)

// Record names the session that registered and the folder it works in.
type Record struct {
	// ID is the id that the provider gives the session.
	ID  string `json:"id"`
	Dir string `json:"dir"`
}

func leadFolder(home string) string { return filepath.Join(home, "sessions", protocol.Lead) }

// Path is the file that holds the record.
func Path(home string) string { return filepath.Join(leadFolder(home), "registered.json") }

// AddressPath is the file that holds the terminal address of the lead.
// A lead that sfy runs in the operator's terminal has one too, and it has no record.
func AddressPath(home string) string { return filepath.Join(leadFolder(home), "address") }

// PidPath is the file that names the process of a lead that sfy runs in the operator's terminal.
func PidPath(home string) string { return filepath.Join(leadFolder(home), "pid") }

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
	if err := os.MkdirAll(leadFolder(home), 0o700); err != nil {
		return err
	}
	return os.WriteFile(Path(home), append(b, '\n'), 0o600)
}

// Folder is the registration of one factory folder.
type Folder struct{ Home string }

// Registered reports whether a session registered as the lead.
func (f Folder) Registered() bool {
	_, ok := Read(f.Home)
	return ok
}

// Clear removes the record, the recorded address and the process record. Missing files are not an error.
func (f Folder) Clear() error {
	var errs []error
	for _, path := range []string{Path(f.Home), AddressPath(f.Home), PidPath(f.Home)} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
