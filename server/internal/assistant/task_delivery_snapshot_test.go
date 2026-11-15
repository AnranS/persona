package assistant

import (
	"context"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestSnapshotLiveConcernDoesNotAwaitReview(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind string
		want bool
	}{
		{name: "open concern", kind: db.KindConcern, want: false},
		{name: "ordinary live delivery", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			todo := Todo{Kind: tc.kind, Delivery: &TodoDelivery{State: deliveryLive, At: time.Now()}}
			if got := deliveryAwaitsReview(todo, nil); got != tc.want {
				t.Fatalf("deliveryAwaitsReview() = %v, want %v", got, tc.want)
			}
		})
	}
}

// The real snapshot projection, not a hand-built template value: delivered work and owner waits use the same words as
// the to-do pages, including a task with no attached Agent. Accepted work is completed, not awaiting review (#5732).
func TestSnapshotTasksUseDeliveryAndOwnerWaitStates(t *testing.T) {
	s, _ := newQuietService(t)
	ctx := context.Background()
	delivered, err := s.createTodo(ctx, "Snapshot delivery", "", "conv1", "")
	if err != nil {
		t.Fatal(err)
	}
	s.superviseTodo(ctx, delivered.ID, "conv1", []string{"result verified"}, 30)
	if _, _, err := s.updateSupervision(ctx, delivered.ID, func(v *Supervision) bool { v.State = reviewPending; return true }); err != nil {
		t.Fatal(err)
	}
	s.setTaskDelivery(ctx, delivered.ID, TodoDelivery{State: deliveryLive, At: time.Now()})
	live, err := s.createTodo(ctx, "Live without a ledger", "", "conv1", "")
	if err != nil {
		t.Fatal(err)
	}
	s.setTaskDelivery(ctx, live.ID, TodoDelivery{State: deliveryLive, At: time.Now()})
	s.superviseTodo(ctx, live.ID, "conv1", []string{"result verified"}, 30)

	byID := func() map[string]struct{ State, Health string } {
		out := map[string]struct{ State, Health string }{}
		for _, task := range s.snapshotTasks(ctx, "https://hub.example.com") {
			out[task.ID] = struct{ State, Health string }{task.State, task.Health}
		}
		return out
	}
	got := byID()
	if got[delivered.ID].State != "已交付·待看" || got[delivered.ID].Health != "已交付·待看" {
		t.Fatalf("pending review projection = %+v", got[delivered.ID])
	}
	if got[live.ID].State != "已交付·待看" || got[live.ID].Health != "已交付·待看" {
		t.Fatalf("unaccepted live projection = %+v", got[live.ID])
	}

	if _, _, err := s.updateSupervision(ctx, delivered.ID, func(v *Supervision) bool { v.State = reviewNeedsUser; return true }); err != nil {
		t.Fatal(err)
	}
	if got := byID()[delivered.ID]; got.State != "等你处理" || got.Health != "等你处理" {
		t.Fatalf("owner-wait projection = %+v", got)
	}
	if _, err := runJSON(t, s, "conv1", "task_review", map[string]any{"todo_id": delivered.ID, "verdict": "accepted", "note": "verified",
		"checks": completeReviewChecks(todoNow(s, delivered.ID))}); err != nil {
		t.Fatal(err)
	}
	if got := byID()[delivered.ID]; got.State != "已完成" || got.Health != "已验收" {
		t.Fatalf("accepted projection = %+v", got)
	}
}

func TestDeliveryAwaitsReviewExcludesLiveConcern(t *testing.T) {
	live := Todo{Delivery: &TodoDelivery{State: deliveryLive, At: time.Now()}}
	if !deliveryAwaitsReview(live, nil) {
		t.Fatal("an ordinary live delivery at rest must await review")
	}
	concern := live
	concern.Kind = db.KindConcern
	if deliveryAwaitsReview(concern, nil) {
		t.Fatal("an open concern with live delivery was projected as awaiting review")
	}
}

// Acceptance still stands after the owner reopens a task until a newer review changes it. Live delivery must not send
// that accepted work back to awaiting review in either State or Health (#5732).
func TestSnapshotReopenedAcceptedLiveDoesNotAwaitReview(t *testing.T) {
	s, _ := newQuietService(t)
	ctx := context.Background()
	accepted, err := s.createTodo(ctx, "Accepted then reopened", "", "conv1", "")
	if err != nil {
		t.Fatal(err)
	}
	s.superviseTodo(ctx, accepted.ID, "conv1", []string{"result verified"}, 30)
	s.setTaskDelivery(ctx, accepted.ID, TodoDelivery{State: deliveryLive, At: time.Now()})
	if _, err := runJSON(t, s, "conv1", "task_review", map[string]any{"todo_id": accepted.ID, "verdict": "accepted", "note": "verified",
		"checks": completeReviewChecks(todoNow(s, accepted.ID))}); err != nil {
		t.Fatal(err)
	}
	if got := todoNow(s, accepted.ID); got.DoneAt == nil {
		t.Fatal("accepted review did not close the task before the reopen")
	}
	if err := s.reopenTodo(ctx, accepted.ID); err != nil {
		t.Fatal(err)
	}
	s.setTaskDelivery(ctx, accepted.ID, TodoDelivery{State: deliveryLive, At: time.Now()})
	if got := todoNow(s, accepted.ID); got.DoneAt != nil || got.ReopenedAt == nil || !acceptanceStands(got) {
		t.Fatalf("reopened acceptance fixture = %+v", got)
	}
	ordinary, err := s.createTodo(ctx, "Ordinary unaccepted live", "", "conv1", "")
	if err != nil {
		t.Fatal(err)
	}
	s.superviseTodo(ctx, ordinary.ID, "conv1", []string{"result verified"}, 30)
	s.setTaskDelivery(ctx, ordinary.ID, TodoDelivery{State: deliveryLive, At: time.Now()})

	got := snapshotStatesForTest(s, ctx)
	if got[accepted.ID].State != "已完成" || got[accepted.ID].Health != "正常" {
		t.Fatalf("reopened accepted live projection = %+v, want completed/normal", got[accepted.ID])
	}
	if got[ordinary.ID].State != "已交付·待看" || got[ordinary.ID].Health != "已交付·待看" {
		t.Fatalf("ordinary unaccepted live control = %+v", got[ordinary.ID])
	}
}

// Seen files only an ordinary finished turn. Pending review, supervision and the otherwise identical unseen turn stay
// awaiting review through the real snapshot projection (#5707, #5732).
func TestSnapshotOrdinarySeenFinishedTurnIsDone(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	makeTask := func(title, sid string) Todo {
		t.Helper()
		todo, err := s.createTodo(ctx, title, "", "conv1", "")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.linkTodoAgent(ctx, todo.ID, "box", sid, title, "/code/app", "claude"); err != nil {
			t.Fatal(err)
		}
		return todo
	}
	seen := makeTask("Ordinary seen", "s-seen")
	unseen := makeTask("Ordinary unseen", "s-unseen")
	pending := makeTask("Pending review seen", "s-pending")
	supervised := makeTask("Supervised seen", "s-supervised")
	for _, todo := range []Todo{pending, supervised} {
		s.superviseTodo(ctx, todo.ID, "conv1", []string{"result verified"}, 30)
	}
	if _, _, err := s.updateSupervision(ctx, pending.ID, func(v *Supervision) bool { v.State = reviewPending; return true }); err != nil {
		t.Fatal(err)
	}
	for _, todo := range []Todo{seen, pending, supervised} {
		if _, err := s.seeTodo(ctx, todo.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	moved := time.Now().Add(-time.Hour)
	sessions := make([]proto.Session, 0, 4)
	for _, sid := range []string{"s-seen", "s-unseen", "s-pending", "s-supervised"} {
		sessions = append(sessions, proto.Session{SessionID: sid, MachineID: "box", Cwd: "/code/app", Agent: "claude",
			Status: proto.StatusDone, State: proto.StateUnread, LastText: "finished", StateSince: moved, UpdatedAt: moved})
	}
	f.setSessions("box", sessions...)

	got := snapshotStatesForTest(s, ctx)
	if got[seen.ID].State != "已完成" || got[seen.ID].Health != "" {
		t.Fatalf("ordinary Seen projection = %+v, want completed", got[seen.ID])
	}
	for _, todo := range []Todo{unseen, pending, supervised} {
		if got[todo.ID].State != "已交付·待看" {
			t.Fatalf("%s control = %+v, want awaiting review", todo.Title, got[todo.ID])
		}
	}
}

func snapshotStatesForTest(s *Service, ctx context.Context) map[string]struct{ State, Health string } {
	out := map[string]struct{ State, Health string }{}
	for _, task := range s.snapshotTasks(ctx, "https://hub.example.com") {
		out[task.ID] = struct{ State, Health string }{task.State, task.Health}
	}
	return out
}
