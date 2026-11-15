package assistant

import (
	"context"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

func resetTurnLedgerForIdleReplayTest(s *Service) {
	b := &s.tasks.turns
	b.mu.Lock()
	b.loaded, b.doc = false, taskTurnsDoc{}
	b.mu.Unlock()
}

func idlePendingForReplayTest(s *Service, g superviseSignal) bool {
	b := &s.supervise
	b.mu.Lock()
	defer b.mu.Unlock()
	cur, ok := b.pending[g.id()]
	return ok && cur.Key == g.Key
}

// A ledger-only idle is the remaining durable form after a manager stopped between judgment and gathering. Resuming
// work acknowledges exactly that old turn; a genuinely newer stopped turn still replays after its own restart.
func TestWorkingRetractsLedgerIdleAcrossRestart(t *testing.T) {
	oldRecoverMost := turnRecoverMost
	turnRecoverMost = 0 // exercise the ledger replay itself, without a transcript-recovery goroutine
	t.Cleanup(func() { turnRecoverMost = oldRecoverMost })
	st := planUpdateStore{&planMemStore{memStore: &memStore{}, plans: map[string]db.TaskPlanRow{}}}
	s, _, todo := newSupervisedServiceOn(t, st)
	s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
	ctx := context.Background()
	if _, _, err := s.updateSupervision(ctx, todo.ID, func(v *Supervision) bool {
		v.State = reviewPending
		return true
	}); err != nil {
		t.Fatal(err)
	}
	agent := proto.Session{MachineID: "box", SessionID: "s1", Status: proto.StatusIdle, UpdatedAt: time.Now().UTC()}
	old := superviseSignal{Todo: todo.ID, Kind: sigIdle, Key: "ledger-old-stop", Machine: "box", Detail: "old ledger idle", At: time.Now()}
	markLeaderTurnSignalled(t, s, old)
	if idlePendingForReplayTest(s, old) {
		t.Fatal("the ledger-only fixture was gathered before the restart")
	}

	s.supervisedWorking(ctx, todoNow(s, todo.ID))
	simulateSupervisionRestart(t, s)
	resetTurnLedgerForIdleReplayTest(s)
	s.watchTurn(ctx, todoNow(s, todo.ID), []proto.Session{agent})
	if idlePendingForReplayTest(s, old) {
		t.Error("the previous stretch's ledger-only idle replayed after work resumed and the manager restarted")
	}
	if got := storedSupervisePending(t, s)[old.id()]; got.Key == old.Key {
		t.Errorf("the replayed old idle became durable again: %+v", got)
	}

	newer := superviseSignal{Todo: todo.ID, Kind: sigIdle, Key: "ledger-new-stop", Machine: "box", Detail: "new ledger idle", At: time.Now()}
	markLeaderTurnSignalled(t, s, newer)
	simulateSupervisionRestart(t, s)
	resetTurnLedgerForIdleReplayTest(s)
	s.watchTurn(ctx, todoNow(s, todo.ID), []proto.Session{agent})
	if !idlePendingForReplayTest(s, newer) {
		t.Error("the genuinely newer post-resume ledger idle did not replay")
	}
	if idlePendingForReplayTest(s, old) {
		t.Error("the newer ledger replay also revived the previous stretch's idle")
	}
}
