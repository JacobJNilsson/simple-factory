package registration

import (
	"os"
	"testing"
)

func TestWriteThenReadReturnsTheRecord(t *testing.T) {
	home := t.TempDir()
	if _, ok := Read(home); ok {
		t.Fatal("a new folder has a record")
	}

	if err := Write(home, Record{ID: "abc", Dir: "/work"}); err != nil {
		t.Fatal(err)
	}

	got, ok := Read(home)
	if !ok || got != (Record{ID: "abc", Dir: "/work"}) {
		t.Fatalf("got %+v, %v", got, ok)
	}
	if !(Folder{Home: home}).Registered() {
		t.Error("the folder reports no registration")
	}
}

func TestReadIgnoresAFileWithoutAnID(t *testing.T) {
	home := t.TempDir()
	if err := Write(home, Record{Dir: "/work"}); err != nil {
		t.Fatal(err)
	}

	if _, ok := Read(home); ok {
		t.Fatal("a record without an id counts as registered")
	}
}

func TestClearRemovesTheRecordAndTheAddress(t *testing.T) {
	home := t.TempDir()
	if err := Write(home, Record{ID: "abc"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(AddressPath(home), []byte("dev\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := (Folder{Home: home}).Clear(); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{Path(home), AddressPath(home)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s still exists: %v", path, err)
		}
	}
	if err := (Folder{Home: home}).Clear(); err != nil {
		t.Fatalf("a second clear fails: %v", err)
	}
}
