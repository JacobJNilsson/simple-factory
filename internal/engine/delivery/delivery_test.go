package delivery_test

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/engine/delivery"
	"github.com/JacobJNilsson/simple-factory/internal/store"
)

const (
	worker       = "worker-t1"
	newNote      = "New message. Run: sfy inbox"
	stuckNote    = "You have undelivered notes. Run: sfy inbox"
	reminderText = "Your turn ended without a report."
)

// fakePanes records the text that sfy types.
type fakePanes struct {
	sent     []string
	dead     map[string]bool
	sendErr  error
	aliveErr error
}

func (f *fakePanes) Send(name, text string) error {
	if f.sendErr != nil {
		return f.sendErr
	}
	f.sent = append(f.sent, name+": "+text)
	return nil
}

func (f *fakePanes) Alive(name string) (bool, error) {
	if f.aliveErr != nil {
		return false, f.aliveErr
	}
	return !f.dead[name], nil
}

// fakeChannel takes text without typing. It keeps the ids of the messages that it took, until take marks them read.
type fakeChannel struct {
	mb         store.Store
	canDeliver bool
	deliverErr error
	delivered  []string
	pending    map[string][]string
	stuck      bool
	drained    string
	drainErr   error
}

func (f *fakeChannel) Deliver(name, text, messageID string) (bool, error) {
	if f.deliverErr != nil {
		return false, f.deliverErr
	}
	if !f.canDeliver {
		return false, nil
	}
	f.delivered = append(f.delivered, name+": "+text)
	if messageID != "" {
		f.pending[name] = append(f.pending[name], messageID)
	}
	return true, nil
}

func (f *fakeChannel) Stuck(string) bool { return f.stuck }

func (f *fakeChannel) Drain(string) (string, error) {
	if f.drainErr != nil {
		return "", f.drainErr
	}
	d := f.drained
	f.drained = ""
	return d, nil
}

// take reads the delivered messages of a session, as the note hook of the provider does.
func (f *fakeChannel) take(t *testing.T, name string) {
	t.Helper()
	if err := f.mb.MarkRead(name, f.pending[name]); err != nil {
		t.Fatal(err)
	}
	f.pending[name] = nil
}

// rig holds a Delivery with its fakes and a clock that the test sets.
type rig struct {
	t      *testing.T
	st     store.Store
	panes  *fakePanes
	ch     *fakeChannel
	d      *delivery.Delivery
	now    time.Time
	log    bytes.Buffer
	seq    int
	norun  map[string]bool
	regist bool
}

// newRig builds a Delivery with a resend of five minutes and a grace of four seconds.
func newRig(t *testing.T) *rig {
	t.Helper()
	return newRigWith(t, 4*time.Second, true)
}

func newRigWith(t *testing.T, grace time.Duration, withChannel bool) *rig {
	t.Helper()
	st := store.Files(t.TempDir())
	r := &rig{t: t, st: st, panes: &fakePanes{}, now: time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC), norun: map[string]bool{}}
	r.ch = &fakeChannel{mb: st, pending: map[string][]string{}}
	deps := delivery.Deps{
		Panes: r.panes, Mailbox: st, Now: func() time.Time { return r.now },
		Logf:       func(format string, args ...any) { fmt.Fprintf(&r.log, format+"\n", args...) },
		Registered: func() bool { return r.regist },
		Runs:       func(name string) bool { return !r.norun[name] },
		Resend:     5 * time.Minute, Grace: grace,
	}
	if withChannel {
		deps.Channel = r.ch
	}
	r.d = delivery.New(deps)
	return r
}

func (r *rig) advance(d time.Duration) { r.now = r.now.Add(d) }

// store keeps a message for a receiver without telling the Delivery.
func (r *rig) store(to, body string) {
	r.t.Helper()
	r.seq++
	m := store.Message{ID: fmt.Sprintf("%06d", r.seq), From: "lead", To: to, Body: body, Created: r.now}
	if err := r.st.Send(m); err != nil {
		r.t.Fatal(err)
	}
}

// message stores a message and tells the Delivery, as the engine does.
func (r *rig) message(to, body string) {
	r.t.Helper()
	r.store(to, body)
	if err := r.d.Message(to, "new message"); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) turnEnded(name string) {
	r.t.Helper()
	if err := r.d.TurnEnded(name, ""); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) idle(name string) {
	r.t.Helper()
	if err := r.d.Idle(name); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) tick(names ...string) {
	r.t.Helper()
	if err := r.d.Tick(names); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) unread(name string) []store.Message {
	r.t.Helper()
	msgs, err := r.st.Unread(name)
	if err != nil {
		r.t.Fatal(err)
	}
	return msgs
}

func (r *rig) wantTyped(want ...string) {
	r.t.Helper()
	if !slices.Equal(r.panes.sent, want) {
		r.t.Fatalf("typed %q, want %q", r.panes.sent, want)
	}
}

func (r *rig) wantNothingTyped() {
	r.t.Helper()
	if len(r.panes.sent) != 0 {
		r.t.Fatalf("typed %q", r.panes.sent)
	}
}

func TestAMessageForASessionThatWaitsGoesThroughTheChannelWithItsSenderAndText(t *testing.T) {
	r := newRig(t)
	r.ch.canDeliver = true
	r.turnEnded(worker)

	r.message(worker, "Please rebase.")

	if want := []string{worker + ": From lead:\nPlease rebase."}; !slices.Equal(r.ch.delivered, want) {
		t.Fatalf("delivered %q, want %q", r.ch.delivered, want)
	}
	r.wantNothingTyped()
}

func TestADeliveredMessageStaysUnreadUntilTheSessionTakesIt(t *testing.T) {
	r := newRig(t)
	r.ch.canDeliver = true
	r.turnEnded(worker)

	r.message(worker, "Please rebase.")

	if got := r.unread(worker); len(got) != 1 {
		t.Fatalf("unread after delivery: %+v", got)
	}
	out, err := r.d.Inbox(worker)
	if err != nil || out != "From lead:\nPlease rebase.\n\n" {
		t.Fatalf("inbox shows %q, %v", out, err)
	}
}

func TestQueuedMessagesAreLoggedAsQueuedAndNotAsDelivered(t *testing.T) {
	r := newRig(t)
	r.ch.canDeliver = true
	r.turnEnded(worker)

	r.message(worker, "Please rebase.")

	if got := r.log.String(); !strings.Contains(got, "messages queued in the note file of worker-t1") || strings.Contains(got, "delivered to") {
		t.Fatalf("log %q", got)
	}
}

func TestASessionWhoseHookLeavesTheMessageUnreadGetsTheNoteTyped(t *testing.T) {
	r := newRig(t)
	r.ch.canDeliver = true
	r.turnEnded(worker)
	r.message(worker, "Please rebase.")
	r.advance(5 * time.Second)

	r.tick(worker)

	r.wantTyped(worker + ": " + newNote)
	if !strings.Contains(r.log.String(), "note typed to worker-t1: the hook left its messages unread") {
		t.Fatalf("log %q", r.log.String())
	}
}

func TestNoNoteIsTypedWhileTheHookStillHasTime(t *testing.T) {
	r := newRig(t)
	r.ch.canDeliver = true
	r.turnEnded(worker)
	r.message(worker, "Please rebase.")
	r.advance(time.Second)

	r.tick(worker)

	r.wantNothingTyped()
}

func TestAMessageThatTheHookTookIsLoggedAndNeverTyped(t *testing.T) {
	r := newRig(t)
	r.ch.canDeliver = true
	r.turnEnded(worker)
	r.message(worker, "Please rebase.")
	r.ch.take(t, worker)
	r.advance(time.Minute)

	r.tick(worker)

	if len(r.panes.sent) != 0 || !strings.Contains(r.log.String(), "messages taken by the hook of worker-t1") {
		t.Fatalf("typed %q, log %q", r.panes.sent, r.log.String())
	}
}

func TestAQueuedMessageThatTheTurnEndDrainTookIsNeverTyped(t *testing.T) {
	r := newRig(t)
	r.ch.canDeliver = true
	r.turnEnded(worker)
	r.d.Busy(worker)
	r.message(worker, "Please rebase.")
	// The Stop hook drains the note, marks the message read and reports busy, because the turn goes on.
	r.ch.take(t, worker)
	r.d.Busy(worker)

	r.advance(10 * time.Minute)
	r.tick(worker)

	if len(r.panes.sent) != 0 || !strings.Contains(r.log.String(), "messages taken by the hook of worker-t1") {
		t.Fatalf("typed %q, log %q", r.panes.sent, r.log.String())
	}
}

func TestInboxShowsTheTextThatNoHookDrained(t *testing.T) {
	r := newRig(t)
	r.ch.drained = "Task t1 is approved."

	out, err := r.d.Inbox(worker)

	if err != nil || out != "Task t1 is approved.\n" {
		t.Fatalf("inbox shows %q, %v", out, err)
	}
}

func TestInboxShowsTheMessagesWhenTheDrainFails(t *testing.T) {
	r := newRig(t)
	r.store(worker, "Please rebase.")
	r.ch.drainErr = errors.New("no folder")

	out, err := r.d.Inbox(worker)

	if err != nil || out != "From lead:\nPlease rebase.\n\n" {
		t.Fatalf("inbox shows %q, %v", out, err)
	}
	if got := r.unread(worker); len(got) != 0 {
		t.Fatalf("unread %+v", got)
	}
}

func TestInboxReturnsMessagesOldestFirstAndThenNothing(t *testing.T) {
	r := newRig(t)
	r.store(worker, "first")
	r.store(worker, "second")

	a, errA := r.d.Inbox(worker)
	b, errB := r.d.Inbox(worker)

	if errA != nil || errB != nil {
		t.Fatalf("errors %v, %v", errA, errB)
	}
	if i, j := strings.Index(a, "first"), strings.Index(a, "second"); i < 0 || j < i {
		t.Fatalf("order: %q", a)
	}
	if b != "" {
		t.Fatalf("second inbox: %q", b)
	}
}

func TestInboxWithoutAChannelShowsTheMessages(t *testing.T) {
	r := newRigWith(t, time.Second, false)
	r.store(worker, "Please rebase.")

	out, err := r.d.Inbox(worker)

	if err != nil || out != "From lead:\nPlease rebase.\n\n" {
		t.Fatalf("inbox shows %q, %v", out, err)
	}
}

func TestATypedNoteForASessionWithStuckTextPointsToInbox(t *testing.T) {
	r := newRig(t)
	r.ch.stuck = true

	if err := r.d.TurnEnded(worker, reminderText); err != nil {
		t.Fatal(err)
	}

	r.wantTyped(worker+": "+reminderText, worker+": "+stuckNote)
}

func TestTheReminderGoesThroughTheChannelWhenItCan(t *testing.T) {
	r := newRig(t)
	r.ch.canDeliver = true

	if err := r.d.TurnEnded(worker, reminderText); err != nil {
		t.Fatal(err)
	}

	if want := []string{worker + ": " + reminderText}; !slices.Equal(r.ch.delivered, want) {
		t.Fatalf("delivered %q, want %q", r.ch.delivered, want)
	}
	r.wantNothingTyped()
}

func TestSeveralUnreadMessagesAreDeliveredOneByOne(t *testing.T) {
	r := newRig(t)
	r.message(worker, "one")
	r.message(worker, "two")
	r.ch.canDeliver = true

	r.turnEnded(worker)

	if want := []string{worker + ": From lead:\none", worker + ": From lead:\ntwo"}; !slices.Equal(r.ch.delivered, want) {
		t.Fatalf("delivered %q, want %q", r.ch.delivered, want)
	}
}

func TestAMessageTheChannelCannotDeliverIsTypedAndStaysUnread(t *testing.T) {
	r := newRig(t)
	r.turnEnded(worker)

	r.message(worker, "Please rebase.")

	r.wantTyped(worker + ": " + newNote)
	if got := r.unread(worker); len(got) != 1 {
		t.Fatalf("unread %+v", got)
	}
}

func TestAMessageWithNoChannelIsTyped(t *testing.T) {
	r := newRigWith(t, time.Second, false)
	r.turnEnded(worker)

	r.message(worker, "Please rebase.")

	r.wantTyped(worker + ": " + newNote)
}

func TestAFailedDeliveryFallsBackToTheTypedNote(t *testing.T) {
	r := newRig(t)
	r.ch.deliverErr = errors.New("disk full")
	r.turnEnded(worker)

	r.message(worker, "Please rebase.")

	r.wantTyped(worker + ": " + newNote)
	if got := r.unread(worker); len(got) != 1 {
		t.Fatalf("unread %+v", got)
	}
}

func TestABusySessionWithAHookGetsItsMessageQueuedAndNeverTyped(t *testing.T) {
	r := newRig(t)
	r.ch.canDeliver = true
	r.turnEnded(worker)
	r.d.Busy(worker)

	r.message(worker, "Please rebase.")
	r.advance(time.Minute)
	r.tick(worker)

	if want := []string{worker + ": From lead:\nPlease rebase."}; !slices.Equal(r.ch.delivered, want) {
		t.Fatalf("delivered %q, want %q", r.ch.delivered, want)
	}
	r.wantNothingTyped()
}

func TestABusySessionWithoutAHookGetsNothingTypedUntilItsTurnEnds(t *testing.T) {
	r := newRig(t)
	r.turnEnded(worker)
	r.d.Busy(worker)

	r.message(worker, "Please rebase.")
	r.advance(time.Minute)
	r.tick(worker)
	r.wantNothingTyped()

	r.turnEnded(worker)
	r.wantTyped(worker + ": " + newNote)
}

func TestANoteStaysQueuedAfterTheGraceWhileTheTurnRuns(t *testing.T) {
	r := newRig(t)
	r.ch.canDeliver = true
	r.turnEnded(worker)
	r.message(worker, "Please rebase.")
	r.d.Busy(worker)

	r.advance(time.Minute)
	r.tick(worker)

	r.wantNothingTyped()
}

func TestTheTurnEndGivesTheHookAFreshGraceBeforeTheNoteIsTyped(t *testing.T) {
	r := newRig(t)
	r.ch.canDeliver = true
	r.turnEnded(worker)
	r.d.Busy(worker)
	r.message(worker, "Please rebase.")
	r.advance(time.Minute)
	r.turnEnded(worker)

	r.tick(worker)
	if len(r.panes.sent) != 0 {
		t.Fatalf("typed %q before the grace ended", r.panes.sent)
	}
	r.advance(5 * time.Second)
	r.tick(worker)

	r.wantTyped(worker + ": " + newNote)
}

func TestTheNoteFallbackWaitsForTheConfiguredGrace(t *testing.T) {
	cases := []struct {
		name    string
		elapsed time.Duration
		typed   bool
	}{
		{"not yet", 9*time.Minute + 59*time.Second, false},
		{"reached", 10 * time.Minute, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRigWith(t, 10*time.Minute, true)
			r.ch.canDeliver = true
			r.turnEnded(worker)
			r.message(worker, "Please rebase.")

			r.advance(c.elapsed)
			r.tick(worker)

			if got := len(r.panes.sent) > 0; got != c.typed {
				t.Fatalf("typed %q, want typed %v", r.panes.sent, c.typed)
			}
		})
	}
}

func TestABurstOfMessagesToASessionThatWaitsIsDeliveredOnceEach(t *testing.T) {
	r := newRig(t)
	r.ch.canDeliver = true
	r.turnEnded(worker)

	for _, body := range []string{"one", "two", "three"} {
		r.message(worker, body)
	}

	want := []string{worker + ": From lead:\none", worker + ": From lead:\ntwo", worker + ": From lead:\nthree"}
	if !slices.Equal(r.ch.delivered, want) || len(r.panes.sent) != 0 {
		t.Fatalf("delivered %q, typed %q", r.ch.delivered, r.panes.sent)
	}
}

func TestABurstOfMessagesToABusySessionIsDeliveredOnceEach(t *testing.T) {
	r := newRig(t)
	r.ch.canDeliver = true
	r.turnEnded(worker)
	r.d.Busy(worker)

	for _, body := range []string{"one", "two", "three"} {
		r.message(worker, body)
	}
	r.advance(time.Minute)
	r.tick(worker)

	want := []string{worker + ": From lead:\none", worker + ": From lead:\ntwo", worker + ": From lead:\nthree"}
	if !slices.Equal(r.ch.delivered, want) || len(r.panes.sent) != 0 {
		t.Fatalf("delivered %q, typed %q", r.ch.delivered, r.panes.sent)
	}
}

func TestARegisteredMainAgentKeepsTheTypedNote(t *testing.T) {
	r := newRig(t)
	r.ch.canDeliver = true
	r.regist = true
	r.turnEnded("lead")

	r.message("lead", "hi")

	if len(r.ch.delivered) != 0 {
		t.Fatalf("delivered %q", r.ch.delivered)
	}
	r.wantTyped("lead: " + newNote)
}

func TestAMessageToASessionThatWaitsTypesTheNoteOnce(t *testing.T) {
	r := newRig(t)
	r.turnEnded("lead")

	r.message("lead", "one")
	r.message("lead", "two")

	r.wantTyped("lead: " + newNote)
}

func TestAMessageToABusySessionTypesNothing(t *testing.T) {
	r := newRig(t)

	r.message("lead", "one")

	r.wantNothingTyped()
}

func TestTickResendsTheNoteAfterResendAndNotBefore(t *testing.T) {
	r := newRig(t)
	r.turnEnded("lead")
	r.message("lead", "one")

	r.advance(4 * time.Minute)
	r.tick("lead")
	before := len(r.panes.sent)
	r.advance(time.Minute)
	r.tick("lead")

	if before != 1 || len(r.panes.sent) != 2 || r.panes.sent[1] != "lead: "+newNote {
		t.Fatalf("before %d, typed %q", before, r.panes.sent)
	}
}

func TestTickDoesNotResendToASessionThatReadItsMessages(t *testing.T) {
	r := newRig(t)
	r.turnEnded("lead")
	r.message("lead", "one")
	if _, err := r.d.Inbox("lead"); err != nil {
		t.Fatal(err)
	}

	r.advance(time.Hour)
	r.tick("lead")

	if len(r.panes.sent) != 1 {
		t.Fatalf("typed %q", r.panes.sent)
	}
}

func TestAFreshDeliveryResendsAnUnreadNoteAfterResend(t *testing.T) {
	r := newRig(t)
	r.store("lead", "one")
	r.advance(5 * time.Minute)

	r.tick("lead")

	r.wantTyped("lead: " + newNote)
	if got := r.unread("lead"); len(got) != 1 {
		t.Fatalf("unread %+v", got)
	}
}

func TestTickDoesNotResendToMainWhenItsSessionIsGone(t *testing.T) {
	r := newRig(t)
	r.store("lead", "one")
	r.panes.dead = map[string]bool{"lead": true}
	r.advance(time.Hour)

	r.tick("lead")

	r.wantNothingTyped()
}

func TestANoteToAGoneSessionDeliversNothingAndLeavesTheMessageUnread(t *testing.T) {
	r := newRig(t)
	r.ch.canDeliver = true
	r.turnEnded(worker)
	r.panes.dead = map[string]bool{worker: true}

	r.message(worker, "change it")

	if len(r.ch.delivered) != 0 || len(r.panes.sent) != 0 {
		t.Fatalf("delivered %q, typed %q", r.ch.delivered, r.panes.sent)
	}
	if got := r.unread(worker); len(got) != 1 {
		t.Fatalf("unread %+v", got)
	}
}

func TestTheLogNamesEachNoteItTypesAndWhyItWasTyped(t *testing.T) {
	r := newRig(t)
	r.turnEnded("lead")
	r.message("lead", "one")
	r.advance(5 * time.Minute)
	r.tick("lead")

	lines := strings.Split(strings.TrimSpace(r.log.String()), "\n")
	want := []string{"note typed to lead: new message", "note typed to lead: resend"}
	if !slices.Equal(lines, want) {
		t.Fatalf("log %q, want %q", lines, want)
	}
}

func TestAGoneSessionIsNamedOnceInTheLog(t *testing.T) {
	r := newRig(t)
	r.store("lead", "one")
	r.panes.dead = map[string]bool{"lead": true}

	r.tick("lead")
	r.tick("lead")

	if n := strings.Count(r.log.String(), "note skipped for lead: its session is not alive"); n != 1 {
		t.Fatalf("log %q", r.log.String())
	}
}

func TestIdleTypesTheNoteForUnreadMail(t *testing.T) {
	r := newRig(t)
	r.store(worker, "hi")

	r.idle(worker)

	r.wantTyped(worker + ": " + newNote)
}

func TestIdleWithoutUnreadMailTypesNothing(t *testing.T) {
	r := newRig(t)

	r.idle(worker)

	r.wantNothingTyped()
}

// An agent that cannot read its inbox ends each turn with the same unread message.
// Each turn end must not type the note again.
func TestTurnEndsWithTheSameUnreadMessageTypeOneNote(t *testing.T) {
	r := newRig(t)
	r.store(worker, "hi")

	for range 5 {
		r.turnEnded(worker)
		r.idle(worker)
	}

	r.wantTyped(worker + ": " + newNote)
}

func TestASecondMessageAfterTheNoteBringsOneMoreNote(t *testing.T) {
	r := newRig(t)
	r.store(worker, "one")
	r.turnEnded(worker)
	r.turnEnded(worker)

	r.store(worker, "two")
	for range 3 {
		r.turnEnded(worker)
	}

	if len(r.panes.sent) != 2 {
		t.Fatalf("typed %q", r.panes.sent)
	}
}

func TestTheResendStillFiresForAMessageThatStaysUnreadAfterTurnEnds(t *testing.T) {
	r := newRig(t)
	r.store(worker, "hi")
	r.turnEnded(worker)
	r.turnEnded(worker)

	r.advance(5 * time.Minute)
	r.tick(worker)

	if len(r.panes.sent) != 2 {
		t.Fatalf("typed %q", r.panes.sent)
	}
}

func TestAMessageMidTurnTypesOneNoteAtTheTurnEnd(t *testing.T) {
	r := newRig(t)
	r.store(worker, "hi")
	r.wantNothingTyped()

	for range 3 {
		r.turnEnded(worker)
	}

	r.wantTyped(worker + ": " + newNote)
}

func TestASessionAtAPermissionPromptGetsNoResendBecauseItIsBusy(t *testing.T) {
	r := newRig(t)
	r.store("lead", "hello")
	r.d.Prompted("lead")

	r.advance(5 * time.Minute)
	r.tick("lead")

	r.wantNothingTyped()
}

func TestTurnEndedForASessionThatDoesNotRunRecordsTheWaitAndTypesNothing(t *testing.T) {
	r := newRig(t)
	r.store(worker, "hi")
	r.norun[worker] = true

	if err := r.d.TurnEnded(worker, reminderText); err != nil {
		t.Fatal(err)
	}
	r.wantNothingTyped()

	delete(r.norun, worker)
	r.message(worker, "again")
	r.wantTyped(worker + ": " + newNote)
}

func TestIdleForASessionThatDoesNotRunTypesNothing(t *testing.T) {
	r := newRig(t)
	r.store(worker, "hi")
	r.norun[worker] = true

	r.idle(worker)

	r.wantNothingTyped()
}

func TestANoteToABusySessionTypesNothingAndIdleTypesIt(t *testing.T) {
	r := newRig(t)

	held, err := r.d.Note("lead", "sfy restarted.")

	if err != nil || !held {
		t.Fatalf("held %v, err %v", held, err)
	}
	r.wantNothingTyped()
	r.idle("lead")
	r.wantTyped("lead: sfy restarted.")
}

func TestANoteToASessionThatWaitsIsTypedAtOnce(t *testing.T) {
	r := newRig(t)
	r.idle("lead")

	held, err := r.d.Note("lead", "sfy restarted.")

	if err != nil || held {
		t.Fatalf("held %v, err %v", held, err)
	}
	r.wantTyped("lead: sfy restarted.")
}

func TestANoteGoesThroughTheChannelWhenItCan(t *testing.T) {
	r := newRig(t)
	r.ch.canDeliver = true

	held, err := r.d.Note("lead", "sfy restarted.")

	if err != nil || held || !slices.Equal(r.ch.delivered, []string{"lead: sfy restarted."}) {
		t.Fatalf("held %v, err %v, delivered %q", held, err, r.ch.delivered)
	}
	r.wantNothingTyped()
}

func TestANoteToARegisteredBusyMainWaitsEvenWhenTheChannelCanDeliver(t *testing.T) {
	r := newRig(t)
	r.ch.canDeliver = true
	r.regist = true
	r.d.Busy("lead")

	held, err := r.d.Note("lead", "sfy restarted.")

	if err != nil || !held || len(r.ch.delivered) != 0 {
		t.Fatalf("held %v, err %v, delivered %q", held, err, r.ch.delivered)
	}
	r.wantNothingTyped()
	r.idle("lead")
	r.wantTyped("lead: sfy restarted.")
}

func TestAHeldNoteIsTypedOnceOverTwoTurnEnds(t *testing.T) {
	r := newRig(t)
	if _, err := r.d.Note("lead", "sfy restarted."); err != nil {
		t.Fatal(err)
	}

	r.turnEnded("lead")
	r.turnEnded("lead")

	r.wantTyped("lead: sfy restarted.")
}

func TestAHeldNoteIsTypedAfterResendWhenNoHookCame(t *testing.T) {
	r := newRig(t)
	if _, err := r.d.Note("lead", "sfy restarted."); err != nil {
		t.Fatal(err)
	}
	r.advance(4 * time.Minute)
	r.tick("lead")
	r.wantNothingTyped()

	r.advance(time.Minute)
	r.tick("lead")
	r.advance(10 * time.Minute)
	r.tick("lead")

	r.wantTyped("lead: sfy restarted.")
}

func TestAHeldNoteNeverReachesASessionThatAHookReportedBusy(t *testing.T) {
	r := newRig(t)
	if _, err := r.d.Note("lead", "sfy restarted."); err != nil {
		t.Fatal(err)
	}
	r.d.Busy("lead")
	r.advance(30 * time.Minute)

	r.tick("lead")

	r.wantNothingTyped()
	r.turnEnded("lead")
	r.wantTyped("lead: sfy restarted.")
}

func TestANoteIsNotHeldForADeadSession(t *testing.T) {
	r := newRig(t)
	r.panes.dead = map[string]bool{"lead": true}

	held, err := r.d.Note("lead", "sfy restarted.")
	r.advance(10 * time.Minute)
	r.tick("lead")

	if err != nil || held {
		t.Fatalf("held %v, err %v", held, err)
	}
	r.wantNothingTyped()
}

func TestAHeldNoteIsNotTypedForADeadSession(t *testing.T) {
	r := newRig(t)
	if _, err := r.d.Note("lead", "sfy restarted."); err != nil {
		t.Fatal(err)
	}
	r.panes.dead = map[string]bool{"lead": true}
	r.advance(10 * time.Minute)

	r.tick("lead")

	r.wantNothingTyped()
}

func TestForgetDropsAHeldNote(t *testing.T) {
	r := newRig(t)
	if _, err := r.d.Note(worker, "Read the new prompt."); err != nil {
		t.Fatal(err)
	}

	r.d.Forget(worker)
	r.turnEnded(worker)

	r.wantNothingTyped()
}

func TestForgetMakesASessionBusyAgain(t *testing.T) {
	r := newRig(t)
	r.turnEnded(worker)

	r.d.Forget(worker)
	r.message(worker, "hi")

	r.wantNothingTyped()
}
