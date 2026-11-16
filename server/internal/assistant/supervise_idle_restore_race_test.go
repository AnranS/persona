package assistant

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
)

// The restore takes its durable snapshot before it waits for supervise.mu. Working must be serialized with the whole
// restore, not only with digest admission, or that older snapshot can be inserted after the idle was retracted.
func TestWorkingSerializesIdleRetractionWithPendingRestore(t *testing.T) {
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
	old := superviseSignal{Todo: todo.ID, Kind: sigIdle, Key: "restore-race-old", Detail: "old restored idle", At: time.Now()}
	s.signal(old)
	if got := storedSupervisePending(t, s)[old.id()]; got.Key != old.Key {
		t.Fatalf("the old idle was not durable: %+v", got)
	}
	simulateSupervisionRestart(t, s)

	// restoreSupervisePending has read pendingLoaded and is waiting for the relay lock. Once it has the relay lock its
	// next wait is supervise.mu. Queue working first on supervise.mu so the broken order inserts the old snapshot last.
	s.relays.mu.Lock()
	restoreDone := make(chan struct{})
	go func() {
		s.restoreSupervisePending(ctx)
		close(restoreDone)
	}()
	waitForSuperviseMutex(t, "restoreSupervisePending", &s.relays.mu)
	s.supervise.mu.Lock()
	workingDone := make(chan struct{})
	go func() {
		s.supervisedWorking(ctx, todoNow(s, todo.ID))
		close(workingDone)
	}()
	waitForSuperviseMutex(t, "supervisedWorking", &s.supervise.mu)
	s.relays.mu.Unlock()
	waitForSuperviseMutex(t, "restoreSupervisePending", &s.supervise.mu)
	s.supervise.mu.Unlock()
	<-restoreDone
	<-workingDone

	if idlePendingForReplayTest(s, old) {
		t.Fatal("the restore inserted its older idle snapshot after working retracted it")
	}
}

// Mutex waits are not durably blocking in testing/synctest. Observe the exact mutex in the goroutine stack instead of
// guessing when it has joined the queue; the address distinguishes restore's relay-lock wait from its later book wait.
func waitForSuperviseMutex(t *testing.T, method string, mu *sync.Mutex) {
	t.Helper()
	lock := fmt.Sprintf("lockSlow(%p)", mu)
	waitFor(t, method+" waiting for its test-held mutex", func() bool {
		for _, stack := range goroutineStacks() {
			if strings.Contains(stack, "[sync.Mutex.Lock]") && strings.Contains(stack, lock) &&
				strings.Contains(stack, "(*Service)."+method+"(") {
				return true
			}
		}
		return false
	})
}
