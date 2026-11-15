package store_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/JacobJNilsson/factory/internal/store"
	"github.com/JacobJNilsson/factory/internal/task"
)

var noon = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

func sample(id string, state task.State) task.Task {
	return task.Task{
		ID: id, Title: "Title " + id, Description: "Do it.", Goal: "Done.",
		Protected: []string{"a/b"}, State: state, Round: 1, Source: "main", Updated: noon,
	}
}

func TestCreateThenTaskReturnsTheSameTask(t *testing.T) {
	s := store.Files(t.TempDir())
	in := sample("t1", task.Queued)

	if err := s.Create(in); err != nil {
		t.Fatal(err)
	}
	got, err := s.Task("t1")

	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("got %+v, want %+v", got, in)
	}
}

func TestTaskReturnsErrNotFoundForAnUnknownID(t *testing.T) {
	s := store.Files(t.TempDir())

	_, err := s.Task("t9")

	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestSaveWritesTheNewStateAndTheNewDescription(t *testing.T) {
	s := store.Files(t.TempDir())
	in := sample("t1", task.Queued)
	if err := s.Create(in); err != nil {
		t.Fatal(err)
	}
	in.State, in.Round, in.Description = task.Feedback, 2, "Do it better."

	if err := s.Save(in); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Task("t1")

	if got.State != task.Feedback || got.Round != 2 || got.Description != "Do it better." {
		t.Fatalf("got %+v", got)
	}
}

func TestTasksReturnsTasksOrderedByNumber(t *testing.T) {
	s := store.Files(t.TempDir())
	for _, id := range []string{"t10", "t2", "t1"} {
		if err := s.Create(sample(id, task.Queued)); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.Tasks()

	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, tk := range got {
		ids = append(ids, tk.ID)
	}
	if !reflect.DeepEqual(ids, []string{"t1", "t2", "t10"}) {
		t.Fatalf("got %v", ids)
	}
}

func TestNextIDCountsUpAcrossAReopenedStore(t *testing.T) {
	dir := t.TempDir()
	first := store.Files(dir)
	a, _ := first.NextID()
	b, _ := first.NextID()

	c, err := store.Files(dir).NextID()

	if err != nil {
		t.Fatal(err)
	}
	if a != "t1" || b != "t2" || c != "t3" {
		t.Fatalf("got %s %s %s", a, b, c)
	}
}

func TestAddVerdictWritesAFileNamedByRoundAndResult(t *testing.T) {
	dir := t.TempDir()
	s := store.Files(dir)
	if err := s.Create(sample("t1", task.Review)); err != nil {
		t.Fatal(err)
	}

	if err := s.AddVerdict("t1", 2, false, "Tests fail."); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "tasks", "t1", "verdicts", "2-fail.md"))

	if err != nil || string(body) != "Tests fail." {
		t.Fatalf("got %q, %v", body, err)
	}
}

func TestAddVerdictReturnsErrNotFoundForAnUnknownTask(t *testing.T) {
	s := store.Files(t.TempDir())

	err := s.AddVerdict("t1", 1, true, "ok")

	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestUnreadReturnsOldestFirstAndMarkReadRemovesThem(t *testing.T) {
	s := store.Files(t.TempDir())
	late := store.Message{ID: "m1", From: "main", To: "worker-t1", Body: "late\nline two", Created: noon.Add(time.Minute)}
	early := store.Message{ID: "m2", From: "main", To: "worker-t1", Body: "early", Created: noon}
	for _, m := range []store.Message{late, early} {
		if err := s.Send(m); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.Unread("worker-t1")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []store.Message{early, late}) {
		t.Fatalf("got %+v", got)
	}
	if err := s.MarkRead("worker-t1", []string{"m2"}); err != nil {
		t.Fatal(err)
	}
	left, _ := s.Unread("worker-t1")

	if !reflect.DeepEqual(left, []store.Message{late}) {
		t.Fatalf("left %+v", left)
	}
}

func TestUnreadIsEmptyForAnAgentWithoutAnInbox(t *testing.T) {
	s := store.Files(t.TempDir())

	got, err := s.Unread("main")

	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestWriteStatusWritesStatusFile(t *testing.T) {
	dir := t.TempDir()
	s := store.Files(dir)

	if err := s.WriteStatus("# Status\n"); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "status.md"))

	if string(body) != "# Status\n" {
		t.Fatalf("got %q", body)
	}
}

func TestRenderStatusListsAnEscalatedTaskBeforeALowerNumberedQueuedTask(t *testing.T) {
	tasks := []task.Task{sample("t1", task.Queued), sample("t2", task.Escalated)}

	out := store.RenderStatus(tasks)

	if strings.Index(out, "t2") > strings.Index(out, "t1") {
		t.Fatalf("t2 should come first:\n%s", out)
	}
}

func TestRenderStatusNamesTheWorkerAndReviewerOfATaskInReview(t *testing.T) {
	tk := sample("t3", task.Review)
	tk.Round = 2

	out := store.RenderStatus([]task.Task{tk})

	if !strings.Contains(out, "worker-t3, review-t3-2") {
		t.Fatalf("agents missing:\n%s", out)
	}
}

func TestRenderStatusShowsADashForAgentsOfAQueuedTask(t *testing.T) {
	out := store.RenderStatus([]task.Task{sample("t1", task.Queued)})

	if !strings.Contains(out, "| 1 | - | 2026") {
		t.Fatalf("want a dash in the agents column:\n%s", out)
	}
}

func TestTasksSkipsAnEmptyFolderAndTemporaryFolders(t *testing.T) {
	dir := t.TempDir()
	s := store.Files(dir)
	if err := s.Create(sample("t2", task.Queued)); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"t1", ".tmp-123"} {
		if err := os.MkdirAll(filepath.Join(dir, "tasks", name), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.Tasks()

	if err != nil || len(got) != 1 || got[0].ID != "t2" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestTaskReturnsErrNotFoundForAnEmptyFolder(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "tasks", "t1"), 0o700); err != nil {
		t.Fatal(err)
	}

	_, err := store.Files(dir).Task("t1")

	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestCreateLeavesNoTemporaryFolderBehind(t *testing.T) {
	dir := t.TempDir()

	if err := store.Files(dir).Create(sample("t1", task.Queued)); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "tasks"))

	if len(entries) != 1 || entries[0].Name() != "t1" {
		t.Fatalf("got %v", entries)
	}
}

func TestStateFilesAreReadableOnlyByTheOwner(t *testing.T) {
	dir := t.TempDir()
	s := store.Files(dir)
	if err := s.Create(sample("t1", task.Queued)); err != nil {
		t.Fatal(err)
	}
	if err := s.Send(store.Message{ID: "m1", From: "main", To: "main", Body: "x", Created: noon}); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteStatus("x"); err != nil {
		t.Fatal(err)
	}

	for path, want := range map[string]os.FileMode{
		"tasks/t1/task.md":    0o600,
		"tasks/t1/state.json": 0o600,
		"inbox/main/m1.md":    0o600,
		"status.md":           0o600,
		"tasks/t1":            0o700,
		"inbox/main":          0o700,
	} {
		info, err := os.Stat(filepath.Join(dir, path))
		if err != nil || info.Mode().Perm() != want {
			t.Errorf("%s: mode %v, %v, want %v", path, info.Mode().Perm(), err, want)
		}
	}
}

func TestSendFailsWhenTheMessageIDExistsUnreadOrRead(t *testing.T) {
	s := store.Files(t.TempDir())
	m := store.Message{ID: "m1", From: "main", To: "w", Body: "x", Created: noon}
	if err := s.Send(m); err != nil {
		t.Fatal(err)
	}
	unread := s.Send(m)
	if err := s.MarkRead("w", []string{"m1"}); err != nil {
		t.Fatal(err)
	}

	read := s.Send(m)

	if unread == nil || read == nil {
		t.Fatalf("want errors, got %v and %v", unread, read)
	}
}

func TestSendFailsWhenFromHoldsANewline(t *testing.T) {
	s := store.Files(t.TempDir())

	err := s.Send(store.Message{ID: "m1", From: "a\nb", To: "w", Body: "x", Created: noon})

	if err == nil {
		t.Fatal("want an error")
	}
}

func TestSendFailsWhenToHoldsANewline(t *testing.T) {
	s := store.Files(t.TempDir())

	err := s.Send(store.Message{ID: "m1", From: "a", To: "w\nx", Body: "x", Created: noon})

	if err == nil {
		t.Fatal("want an error")
	}
}

func TestMarkReadIgnoresAnUnknownMessageID(t *testing.T) {
	s := store.Files(t.TempDir())

	err := s.MarkRead("w", []string{"m9"})

	if err != nil {
		t.Fatal(err)
	}
}

func TestCreateFailsForAnExistingID(t *testing.T) {
	s := store.Files(t.TempDir())
	if err := s.Create(sample("t1", task.Queued)); err != nil {
		t.Fatal(err)
	}

	err := s.Create(sample("t1", task.Queued))

	if err == nil {
		t.Fatal("want an error")
	}
}

func TestCreateFailsForAnInvalidID(t *testing.T) {
	s := store.Files(t.TempDir())

	err := s.Create(sample("../x", task.Queued))

	if err == nil {
		t.Fatal("want an error")
	}
}

func TestTaskReturnsErrNotFoundForAnInvalidID(t *testing.T) {
	s := store.Files(t.TempDir())

	_, err := s.Task("../x")

	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestCreateRefusesATaskThatDoesNotParseBackAndKeepsTheOthers(t *testing.T) {
	s := store.Files(t.TempDir())
	if err := s.Create(sample("t1", task.Queued)); err != nil {
		t.Fatal(err)
	}
	bad := sample("t2", task.Queued)
	bad.Description = "Do it.\n## Repo\nelsewhere"

	err := s.Create(bad)
	got, listErr := s.Tasks()

	if err == nil || listErr != nil || len(got) != 1 || got[0].ID != "t1" {
		t.Fatalf("err %v, list %+v, %v", err, got, listErr)
	}
}

func TestSaveRefusesATaskThatDoesNotParseBackAndWritesNothing(t *testing.T) {
	s := store.Files(t.TempDir())
	in := sample("t1", task.Queued)
	if err := s.Create(in); err != nil {
		t.Fatal(err)
	}
	in.Description = "Do it.\n## Repo\nelsewhere"

	err := s.Save(in)
	got, _ := s.Task("t1")

	if err == nil || got.Description != "Do it." {
		t.Fatalf("err %v, got %+v", err, got)
	}
}

func TestSaveTrimsBlankSpaceAroundFieldsAndDropsBlankProtectedEntries(t *testing.T) {
	s := store.Files(t.TempDir())
	in := sample("t1", task.Queued)
	in.Goal = "\n\n  Done.\n\n"
	in.Protected = []string{" a/b ", "", "  "}
	if err := s.Create(in); err != nil {
		t.Fatal(err)
	}

	got, err := s.Task("t1")

	if err != nil || got.Goal != "Done." || !reflect.DeepEqual(got.Protected, []string{"a/b"}) {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestSaveKeepsAWorkersProgressForTheNextRead(t *testing.T) {
	s := store.Files(t.TempDir())
	in := sample("t1", task.Working)
	if err := s.Create(in); err != nil {
		t.Fatal(err)
	}
	in.Progress = "tests written"

	if err := s.Save(in); err != nil {
		t.Fatal(err)
	}
	got, err := s.Task("t1")

	if err != nil {
		t.Fatal(err)
	}
	if got.Progress != "tests written" {
		t.Fatalf("got progress %q", got.Progress)
	}
}

func TestSaveKeepsATasksWorkspaceForTheNextRead(t *testing.T) {
	s := store.Files(t.TempDir())
	in := sample("t1", task.Working)
	if err := s.Create(in); err != nil {
		t.Fatal(err)
	}
	in.Dir = "/work/t1"

	if err := s.Save(in); err != nil {
		t.Fatal(err)
	}
	got, err := s.Task("t1")

	if err != nil {
		t.Fatal(err)
	}
	if got.Dir != "/work/t1" {
		t.Fatalf("got dir %q", got.Dir)
	}
}

func TestVerdictsReturnsTheVerdictsOrderedByRound(t *testing.T) {
	s := store.Files(t.TempDir())
	if err := s.Create(sample("t1", task.Review)); err != nil {
		t.Fatal(err)
	}
	if err := s.AddVerdict("t1", 2, true, "good"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddVerdict("t1", 1, false, "bad"); err != nil {
		t.Fatal(err)
	}

	got, err := s.Verdicts("t1")

	want := []store.Verdict{{Round: 1, Pass: false, Body: "bad"}, {Round: 2, Pass: true, Body: "good"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestVerdictsIsEmptyForATaskWithoutVerdicts(t *testing.T) {
	s := store.Files(t.TempDir())
	if err := s.Create(sample("t1", task.Review)); err != nil {
		t.Fatal(err)
	}

	got, err := s.Verdicts("t1")

	if err != nil || len(got) != 0 {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestVerdictsReturnsErrNotFoundForAnUnknownTask(t *testing.T) {
	_, err := store.Files(t.TempDir()).Verdicts("t9")

	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}
