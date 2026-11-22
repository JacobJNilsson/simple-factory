package engine_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/drop"
	"github.com/JacobJNilsson/simple-factory/internal/task"
)

// dropEnv returns an engine that takes drops from the drop folder of its factory folder.
func dropEnv(t *testing.T) env {
	t.Helper()
	v := newEnv(t)
	f := drop.Folder{Home: v.home}
	if err := f.Prepare(); err != nil {
		t.Fatal(err)
	}
	v.e.SetDrops(f)
	return v
}

// dropFile writes a drop as a sender does, into tmp/ and then by a rename into new/.
func (v env) dropFile(t *testing.T, name, text string) {
	t.Helper()
	tmp := filepath.Join(v.home, "drop", "tmp", name)
	if err := os.WriteFile(tmp, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, filepath.Join(v.home, "drop", "new", name)); err != nil {
		t.Fatal(err)
	}
}

func (v env) pickUp(t *testing.T) {
	t.Helper()
	if err := v.e.PickUpDrops(); err != nil {
		t.Fatal(err)
	}
}

func (v env) dropFolder(t *testing.T, parts ...string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(append([]string{v.home, "drop"}, parts...)...))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

const dropTask = "To: lead\nFrom: alerts\nId: alert-4711\nKind: task\n\n" + taskFile

func TestAMessageDropReachesTheLeadAsAStatedName(t *testing.T) {
	v := dropEnv(t)
	v.dropFile(t, "1-alerts-a1.md", "To: lead\nFrom: alerts\nId: a1\n\nDisk full on db-2\n")

	v.pickUp(t)

	msgs := v.inbox(t, "lead")
	if len(msgs) != 1 || msgs[0].From != "drop:alerts" || msgs[0].Body != "Disk full on db-2\n" {
		t.Fatalf("lead inbox: %+v", msgs)
	}
	ans := v.run("lead", "", "inbox", "")
	mustOK(t, ans)
	if !strings.Contains(ans.Text, "From drop:alerts, a name that the sender stated and nothing checked:") {
		t.Errorf("inbox %q does not mark the stated name", ans.Text)
	}
	if done := v.dropFolder(t, "done", "alerts"); len(done) != 1 || done[0] != "a1.md" {
		t.Errorf("done/alerts holds %v", done)
	}
}

func TestATaskDropCreatesANewTaskWithItsSourceAndTellsTheLead(t *testing.T) {
	v := dropEnv(t)
	v.dropFile(t, "1-alerts-alert-4711.md", dropTask)

	v.pickUp(t)

	tk := v.state(t, "t1")
	if tk.State != task.New || tk.Source != "drop:alerts" {
		t.Fatalf("task %+v", tk)
	}
	msgs := v.inbox(t, "lead")
	if len(msgs) != 1 || msgs[0].From != "sfy" || !strings.Contains(msgs[0].Body, "New task t1") || !strings.Contains(msgs[0].Body, "drop:alerts, a name that the sender stated") {
		t.Fatalf("lead inbox: %+v", msgs)
	}
	if !strings.Contains(v.status(t), "Title (from drop:alerts)") {
		t.Errorf("status.md does not show the source:\n%s", v.status(t))
	}
	list := v.run("lead", "", "task list", "")
	if !strings.Contains(list.Text, "Title (from drop:alerts)") {
		t.Errorf("task list %q", list.Text)
	}
	show := v.run("lead", "", "task show", "", "t1")
	if !strings.Contains(show.Text, "Source: drop:alerts") {
		t.Errorf("task show %q", show.Text)
	}
}

func TestABadDropMovesToRejected(t *testing.T) {
	cases := map[string]string{
		"operator": "To: lead\nFrom: operator\nId: a1\n\nx\n",
		"slash":    "To: lead\nFrom: a/lead\nId: a1\n\nx\n",
		"colon":    "To: lead\nFrom: drop:x\nId: a1\n\nx\n",
		"worker":   "To: worker-t3\nFrom: alerts\nId: a1\n\nx\n",
		"task":     "To: lead\nFrom: alerts\nId: a1\nKind: task\n\nno task file\n",
	}
	for name, text := range cases {
		v := dropEnv(t)
		v.dropFile(t, "1-"+name+".md", text)

		v.pickUp(t)

		if msgs := v.inbox(t, "lead"); len(msgs) != 0 {
			t.Errorf("%s: the lead got %+v", name, msgs)
		}
		if got := v.dropFolder(t, "rejected"); len(got) != 2 {
			t.Errorf("%s: rejected/ holds %v, want the drop and its reason", name, got)
		}
		if left := v.dropFolder(t, "new"); len(left) != 0 {
			t.Errorf("%s: new/ holds %v", name, left)
		}
	}
}

func TestASecondDropWithTheSameSenderAndIdCreatesNothing(t *testing.T) {
	v := dropEnv(t)
	v.dropFile(t, "1-alerts-alert-4711.md", dropTask)
	v.pickUp(t)
	v.dropFile(t, "2-alerts-alert-4711.md", dropTask)

	v.pickUp(t)

	if _, err := v.st.Task("t2"); err == nil {
		t.Error("the duplicate created a task")
	}
	if left := v.dropFolder(t, "new"); len(left) != 0 {
		t.Errorf("new/ holds %v", left)
	}
}

func TestTwoSendersThatDropTheSameIdBothArrive(t *testing.T) {
	v := dropEnv(t)
	v.dropFile(t, "1-alerts-x1.md", "To: lead\nFrom: alerts\nId: x1\n\nfirst\n")
	v.dropFile(t, "1-builds-x1.md", "To: lead\nFrom: builds\nId: x1\n\nsecond\n")

	v.pickUp(t)

	msgs := v.inbox(t, "lead")
	if len(msgs) != 2 || msgs[0].From != "drop:alerts" || msgs[1].From != "drop:builds" {
		t.Fatalf("lead inbox: %+v", msgs)
	}
}

func TestADropWrittenWhileSfyIsDownArrivesAtTheNextStart(t *testing.T) {
	v := newEnv(t)
	f := drop.Folder{Home: v.home}
	if err := f.Prepare(); err != nil {
		t.Fatal(err)
	}
	v.dropFile(t, "1-alerts-a1.md", "To: lead\nFrom: alerts\nId: a1\n\nwhile down\n")

	v.e.SetDrops(f)
	v.pickUp(t)

	if msgs := v.inbox(t, "lead"); len(msgs) != 1 || msgs[0].Body != "while down\n" {
		t.Fatalf("lead inbox: %+v", msgs)
	}
}

func TestATickTakesNewDropsAndLeavesTmpAlone(t *testing.T) {
	v := dropEnv(t)
	half := filepath.Join(v.home, "drop", "tmp", "1-alerts-a1.md")
	if err := os.WriteFile(half, []byte("To: lead\nFrom: alerts\nId: a1\n\nhalf"), 0o600); err != nil {
		t.Fatal(err)
	}
	v.dropFile(t, "2-alerts-a2.md", "To: lead\nFrom: alerts\nId: a2\n\nwhole\n")

	if err := v.e.Tick(); err != nil {
		t.Fatal(err)
	}

	if msgs := v.inbox(t, "lead"); len(msgs) != 1 || msgs[0].Body != "whole\n" {
		t.Fatalf("lead inbox: %+v", msgs)
	}
	if _, err := os.Stat(half); err != nil {
		t.Errorf("the file in tmp/ moved: %v", err)
	}
}
