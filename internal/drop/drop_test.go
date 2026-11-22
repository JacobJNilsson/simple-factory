package drop_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/drop"
)

func TestParseReadsTheHeadersAndTheBody(t *testing.T) {
	d, err := drop.Parse([]byte("To: lead\nFrom: alerts\nId: alert-4711\nKind: task\nX-Extra: kept out\n\n# Disk full\n"))

	if err != nil {
		t.Fatal(err)
	}
	want := drop.Drop{To: "lead", From: "alerts", ID: "alert-4711", Kind: drop.KindTask, Body: "# Disk full\n"}
	if d != want {
		t.Errorf("got %+v, want %+v", d, want)
	}
	if d.Sender() != "drop:alerts" {
		t.Errorf("sender %q", d.Sender())
	}
}

func TestParseTakesAMessageByDefaultAndTheOldNameOfTheLead(t *testing.T) {
	d, err := drop.Parse([]byte("To: main\nFrom: alerts\nId: a1\n\nhello\n"))

	if err != nil || d.Kind != drop.KindMessage || d.To != "lead" {
		t.Fatalf("got %+v, %v", d, err)
	}
}

func TestParseRefusesABadDrop(t *testing.T) {
	cases := map[string]string{
		"operator name":  "To: lead\nFrom: operator\nId: a1\n\nx",
		"sfy name":       "To: lead\nFrom: sfy\nId: a1\n\nx",
		"slash in name":  "To: lead\nFrom: a/lead\nId: a1\n\nx",
		"colon in name":  "To: lead\nFrom: drop:x\nId: a1\n\nx",
		"upper case":     "To: lead\nFrom: Alerts\nId: a1\n\nx",
		"to a worker":    "To: worker-t3\nFrom: alerts\nId: a1\n\nx",
		"no to":          "From: alerts\nId: a1\n\nx",
		"no id":          "To: lead\nFrom: alerts\n\nx",
		"id with slash":  "To: lead\nFrom: alerts\nId: ../a\n\nx",
		"long id":        "To: lead\nFrom: alerts\nId: " + strings.Repeat("a", 65) + "\n\nx",
		"unknown kind":   "To: lead\nFrom: alerts\nId: a1\nKind: command\n\nx",
		"no blank line":  "To: lead\nFrom: alerts\nId: a1\n",
		"empty body":     "To: lead\nFrom: alerts\nId: a1\n\n \n",
		"two from lines": "To: lead\nFrom: alerts\nFrom: other\nId: a1\n\nx",
		"not utf-8":      "To: lead\nFrom: alerts\nId: a1\n\n\xff",
		"too large":      "To: lead\nFrom: alerts\nId: a1\n\n" + strings.Repeat("x", drop.MaxSize),
	}
	for name, raw := range cases {
		if d, err := drop.Parse([]byte(raw)); err == nil {
			t.Errorf("%s: accepted %+v", name, d)
		}
	}
}

func TestParseKeepsAValidSenderOfARefusedDrop(t *testing.T) {
	d, err := drop.Parse([]byte("To: worker-t3\nFrom: alerts\nId: a1\n\nx"))

	if err == nil || d.From != "alerts" || d.ID != "a1" {
		t.Fatalf("got %+v, %v", d, err)
	}
}

func folder(t *testing.T) drop.Folder {
	t.Helper()
	f := drop.Folder{Home: t.TempDir()}
	if err := f.Prepare(); err != nil {
		t.Fatal(err)
	}
	return f
}

// put drops a file the way a sender does: it writes into tmp/ and renames into new/.
func put(t *testing.T, f drop.Folder, name string) string {
	t.Helper()
	tmp := filepath.Join(f.Home, "drop", "tmp", name)
	if err := os.WriteFile(tmp, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.Home, "drop", "new", name)
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNewListsTheDropsInNameOrderAndSkipsHiddenFiles(t *testing.T) {
	f := folder(t)
	b := put(t, f, "2-alerts-b.md")
	a := put(t, f, "1-alerts-a.md")
	put(t, f, ".partial")
	if err := os.WriteFile(filepath.Join(f.Home, "drop", "tmp", "0-alerts-c.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := f.New()

	if err != nil || !slices.Equal(got, []string{a, b}) {
		t.Fatalf("New = %v, %v, want %v", got, err, []string{a, b})
	}
}

func TestDoneRecordsTheSenderAndIdForTheDuplicateCheck(t *testing.T) {
	f := folder(t)
	path := put(t, f, "1-alerts-a1.md")
	d := drop.Drop{From: "alerts", ID: "a1"}
	if f.Seen("alerts", "a1") || f.Known("alerts") {
		t.Fatal("a fresh folder knows the drop")
	}

	if err := f.Done(path, d); err != nil {
		t.Fatal(err)
	}

	if !f.Seen("alerts", "a1") || !f.Known("alerts") || f.Seen("alerts", "a2") || f.Known("other") {
		t.Error("Done did not record exactly this drop")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the drop stays in new/: %v", err)
	}
}

func TestRejectMovesTheDropAndWritesTheReason(t *testing.T) {
	f := folder(t)
	path := put(t, f, "1-alerts-a1.md")

	if err := f.Reject(path, "bad header"); err != nil {
		t.Fatal(err)
	}

	why, err := os.ReadFile(filepath.Join(f.Home, "drop", "rejected", "1-alerts-a1.md.why"))
	if err != nil || strings.TrimSpace(string(why)) != "bad header" {
		t.Errorf("why = %q, %v", why, err)
	}
	if left, _ := f.New(); len(left) != 0 {
		t.Errorf("new/ still holds %v", left)
	}
}

func TestReplyWritesAFileInTheFormatOfADrop(t *testing.T) {
	f := folder(t)

	err := f.Reply("alerts", drop.Reply{ID: "r1", From: "sfy", Kind: drop.ReplyReceipt, Re: "a1", Task: "t4", Body: "Task t4 created."})

	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(f.Home, "drop", "replies", "alerts", "r1.md"))
	if err != nil {
		t.Fatal(err)
	}
	want := "To: alerts\nFrom: sfy\nId: r1\nKind: receipt\nRe: a1\nTask: t4\n\nTask t4 created.\n"
	if string(raw) != want {
		t.Errorf("reply %q, want %q", raw, want)
	}
	if err := f.Reply("../x", drop.Reply{ID: "r2"}); err == nil {
		t.Error("a reply to a bad name was written")
	}
}

func TestPruneRemovesOldRecordsRepliesAndLeftovers(t *testing.T) {
	f := folder(t)
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	old := map[string]time.Time{
		"done/alerts/old.md":    now.Add(-31 * 24 * time.Hour),
		"done/alerts/new.md":    now.Add(-29 * 24 * time.Hour),
		"replies/alerts/old.md": now.Add(-31 * 24 * time.Hour),
		"tmp/stale.md":          now.Add(-25 * time.Hour),
		"tmp/fresh.md":          now.Add(-time.Hour),
	}
	for rel, at := range old {
		path := filepath.Join(f.Home, "drop", rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}

	if err := f.Prune(now); err != nil {
		t.Fatal(err)
	}

	for rel, keep := range map[string]bool{"done/alerts/old.md": false, "done/alerts/new.md": true, "replies/alerts/old.md": false, "tmp/stale.md": false, "tmp/fresh.md": true} {
		_, err := os.Stat(filepath.Join(f.Home, "drop", rel))
		if (err == nil) != keep {
			t.Errorf("%s: exists %v, want %v", rel, err == nil, keep)
		}
	}
}
