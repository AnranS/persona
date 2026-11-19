package assistant

import (
	"context"
	"time"
)

// receiptRecheck binds a delayed look to the exact task-agent association that needed it. A session later linked to
// another task, or the same task reopened for another stretch, must not inherit the old look.
type receiptRecheck struct {
	MachineID  string     `json:"machine_id"`
	SessionID  string     `json:"session_id"`
	TodoID     string     `json:"todo_id"`
	LinkedAt   time.Time  `json:"linked_at"`
	ReopenedAt *time.Time `json:"reopened_at,omitempty"`
}

func (s *Service) newReceiptRecheck(ctx context.Context, machine, sid string, at time.Time) delayedWait {
	r := &receiptRecheck{MachineID: machine, SessionID: sid}
	if t, ok := s.taskOf(ctx, machine, sid); ok {
		for _, a := range t.Agents {
			if a.MachineID == machine && a.SessionID == sid && a.ReleasedAt == nil && a.DismissedAt == nil {
				r.TodoID, r.LinkedAt = t.ID, bindingTime(a.LinkedAt)
				if t.ReopenedAt != nil {
					reopened := bindingTime(*t.ReopenedAt)
					r.ReopenedAt = &reopened
				}
				break
			}
		}
	}
	return delayedWait{ID: receiptRecheckWaitID(machine, sid, at), Kind: delayedReceiptRecheck, At: at, Receipt: r}
}

// persistReceiptRecheckRetry keeps the original dispatch deadline while the initial durable write is unavailable.
// The local timer is armed only after the row is durable, so a restart before that deadline cannot lose the recheck.
func (s *Service) persistReceiptRecheckRetry(ctx context.Context, w delayedWait) {
	for {
		pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		selected, arm, err := s.persistReceiptRecheck(pctx, w)
		cancel()
		if err == nil {
			if arm {
				s.armReceiptRecheck(selected, true)
			}
			return
		}
		s.logf("persist receipt recheck %s: %v", w.ID, err)
		if !waitForDelayedWait(ctx, time.Now().Add(delayedWaitRecoveryDelay)) {
			return
		}
	}
}

// armReceiptRecheck retains the existing earliest-deadline map semantics. Superseded persisted timers lose their
// claim, so replacing the map value cannot make a later look run ahead of the earliest one.
func (s *Service) armReceiptRecheck(w delayedWait, persisted bool) {
	if w.Receipt == nil {
		return
	}
	ref := SessionRef{MachineID: w.Receipt.MachineID, SessionID: w.Receipt.SessionID}
	s.tasks.mu.Lock()
	if s.tasks.limitLooks == nil {
		s.tasks.limitLooks = map[SessionRef]time.Time{}
	}
	if pending, ok := s.tasks.limitLooks[ref]; ok && !pending.After(w.At) && !w.replace {
		s.tasks.mu.Unlock()
		return
	}
	s.tasks.limitLooks[ref] = w.At
	s.tasks.mu.Unlock()
	time.AfterFunc(max(time.Until(w.At), 0), func() {
		s.tasks.mu.Lock()
		if s.tasks.limitLooks[ref].Equal(w.At) {
			delete(s.tasks.limitLooks, ref)
		}
		s.tasks.mu.Unlock()
		s.runReceiptRecheck(s.runCtx(), w, persisted)
	})
}

func (s *Service) runReceiptRecheck(ctx context.Context, w delayedWait, persisted bool) {
	if ctx.Err() != nil || w.Receipt == nil {
		return
	}
	if !persisted {
		s.taskTurnEnded(ctx, w.Receipt.MachineID, w.Receipt.SessionID)
		return
	}
	token := randomHex(16)
	claimed, ok, err := s.claimDelayedWait(ctx, w.ID, token, time.Now())
	if err != nil {
		s.rearmReceiptRecheckAfterClaimError(ctx, w, err, token)
		return
	}
	if !ok {
		return
	}
	current, err := s.receiptRecheckCurrent(ctx, claimed.Receipt)
	if err != nil {
		s.retryClaimedReceiptRecheck(ctx, claimed, token)
		return
	}
	if !current {
		s.completeReceiptRecheck(claimed, token)
		return
	}
	time.Sleep(s.settleDelay(ctx))
	// The binding can change while the turn settles. Recheck it before settlement resolves the session's task;
	// otherwise this old recheck could issue a receipt and judgment for a reopened or relinked task.
	current, err = s.receiptRecheckCurrent(ctx, claimed.Receipt)
	if err != nil {
		s.retryClaimedReceiptRecheck(ctx, claimed, token)
		return
	}
	if !current {
		s.completeReceiptRecheck(claimed, token)
		return
	}
	var unreachable bool
	outcome := s.settleTurnReporting(ctx, claimed.Receipt.MachineID, claimed.Receipt.SessionID, "", &unreachable)
	if outcome == settleLater && unreachable {
		s.retryClaimedReceiptRecheck(ctx, claimed, token)
		return
	}
	s.completeReceiptRecheck(claimed, token)
}

func (s *Service) receiptRecheckCurrent(ctx context.Context, r *receiptRecheck) (bool, error) {
	if r == nil || r.TodoID == "" {
		return true, nil
	}
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return false, err
	}
	t, ok := s.taskOf(ctx, r.MachineID, r.SessionID)
	if !ok || t.ID != r.TodoID || !sameOptionalTime(t.ReopenedAt, r.ReopenedAt) {
		return false, nil
	}
	for _, a := range t.Agents {
		if a.MachineID == r.MachineID && a.SessionID == r.SessionID && a.ReleasedAt == nil && a.DismissedAt == nil {
			return bindingTime(a.LinkedAt).Equal(bindingTime(r.LinkedAt)), nil
		}
	}
	return false, nil
}

func bindingTime(t time.Time) time.Time { return t.UTC().Truncate(time.Microsecond) }

func sameOptionalTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return bindingTime(*a).Equal(bindingTime(*b))
}

func (s *Service) rearmReceiptRecheckAfterClaimError(ctx context.Context, w delayedWait, why error, token string) {
	if ctx.Err() != nil {
		return
	}
	s.logf("claim receipt recheck %s: %v", w.ID, why)
	nextAt := delayedWaitRetryAt()
	released, err := s.releaseReceiptRecheck(ctx, w, token, nextAt)
	if err != nil {
		s.logf("rearm receipt recheck %s: %v", w.ID, err)
		time.AfterFunc(delayedWaitRecoveryDelay, func() { s.rearmReceiptRecheckAfterClaimError(s.runCtx(), w, why, token) })
		return
	}
	if released {
		current, found, err := s.delayedWaitByID(ctx, w.ID)
		if err != nil {
			s.logf("read rearmed receipt recheck %s: %v", w.ID, err)
			time.AfterFunc(delayedWaitRecoveryDelay, func() { s.rearmReceiptRecheckAfterClaimError(s.runCtx(), w, why, token) })
			return
		}
		if found && current.Claim == "" {
			// The failed callback may already have removed or replaced its map entry. Arm the exact
			// persisted deadline and force it past any older entry.
			current.replace = true
			s.armReceiptRecheck(current, true)
		}
	}
}

func (s *Service) retryClaimedReceiptRecheck(ctx context.Context, w delayedWait, token string) {
	if ctx.Err() != nil {
		return
	}
	nextAt := time.Now().Add(receiptRetryAfter)
	released, err := s.releaseReceiptRecheck(ctx, w, token, nextAt)
	if err != nil {
		s.logf("retain receipt recheck %s: %v", w.ID, err)
		// This manager still owns the claim. Retry the release instead of arming a second local callback or
		// leaving the claimed row stranded until an unrelated restart.
		time.AfterFunc(delayedWaitRecoveryDelay, func() { s.retryClaimedReceiptRecheck(s.runCtx(), w, token) })
		return
	}
	if released {
		current, found, err := s.delayedWaitByID(ctx, w.ID)
		if err != nil {
			s.logf("read released receipt recheck %s: %v", w.ID, err)
			time.AfterFunc(delayedWaitRecoveryDelay, func() { s.retryClaimedReceiptRecheck(s.runCtx(), w, token) })
			return
		}
		if found && current.Claim == "" {
			s.armReceiptRecheck(current, true)
		}
	}
}

func (s *Service) completeReceiptRecheck(w delayedWait, token string) {
	if err := s.completeDelayedWait(context.Background(), w.ID, token); err != nil {
		s.logf("finish receipt recheck %s: %v", w.ID, err)
	}
}
