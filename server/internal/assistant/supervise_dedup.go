package assistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/prompts"
)

const superviseDedupKey = "assistant.supervision_dedup"
const supervisePayloadsKey = "assistant.supervision_payloads"
const superviseDedupWindow = 30 * time.Minute

type superviseFingerprint struct {
	Hash      string    `json:"hash"`
	At        time.Time `json:"at"`
	Key       string    `json:"key,omitempty"` // the stretch reserved by this fingerprint; empty before #5732
	Permanent bool      `json:"permanent,omitempty"`
	// Todo bounds inactivity, daily-summary and unhandled-event fingerprints by the task's lifetime.
	// Other non-permanent kinds retain the short semantic-deduplication window.
	Todo string `json:"todo,omitempty"`
	// Payload is used only by superviseBook.payloads, never by the whole-signal deduplication map.
	Payload *superviseEventPayload `json:"payload,omitempty"`
}

type superviseEventPayload struct {
	Criteria, Request, Said, Last string
	Evidence                      []string
}

func superviseEventPayloadKey(g superviseSignal) string {
	key, _ := json.Marshal([]string{"event-payload", g.Recipient, g.Todo})
	return string(key)
}

func superviseContentHash(v any) string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func currentSuperviseEventPayload(e prompts.SuperviseEntry) superviseEventPayload {
	p := superviseEventPayload{
		Criteria: superviseContentHash(e.DoneWhen),
		Request:  superviseContentHash([]any{e.Asked, e.AskedAt, e.Started, e.StartedWhy, e.Dropped}),
		Last:     superviseContentHash([]string{e.AgentID, e.Last}),
	}
	if e.Said != "" {
		p.Said = superviseContentHash(e.Said)
	}
	for _, evidence := range e.Evidence {
		p.Evidence = append(p.Evidence, superviseContentHash(evidence))
	}
	return p
}

func repeatedEvidence(previous, current []string) []string {
	if len(previous) == 0 || len(current) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(previous))
	for _, hash := range previous {
		seen[hash] = true
	}
	var repeated []string
	for _, hash := range current {
		if seen[hash] {
			repeated = append(repeated, hash)
		}
	}
	return repeated
}

// putSupervisionHistories commits whole-signal deduplication and per-recipient payload history together where the
// store supports settings batches. Callers restore both snapshots after a sequential fallback's partial failure.
func (s *Service) putSupervisionHistories(ctx context.Context, dedup, payloads map[string]superviseFingerprint) error {
	st := s.store()
	if st == nil {
		return nil
	}
	if batch, ok := st.(interface {
		PutSettings(context.Context, map[string]any) error
	}); ok {
		return batch.PutSettings(ctx, map[string]any{superviseDedupKey: dedup, supervisePayloadsKey: payloads})
	}
	if err := st.PutSetting(ctx, superviseDedupKey, dedup); err != nil {
		return err
	}
	return st.PutSetting(ctx, supervisePayloadsKey, payloads)
}

func digestFingerprint(e prompts.SuperviseEntry, g superviseSignal) (string, string) {
	if g.Kind == sigTodoReady || g.Kind == sigParentCloseout {
		// One durable wake per to-do and canonical prerequisite/children set, independent of restarts or sweeps.
		key, _ := json.Marshal([]string{g.Todo, g.Kind})
		sum := sha256.Sum256([]byte(g.Key))
		return string(key), hex.EncodeToString(sum[:])
	}
	if g.Kind == sigParkedSummary {
		key, _ := json.Marshal([]string{g.Todo, g.Kind, g.To})
		day, _, _ := strings.Cut(g.Key, "|")
		return string(key), day // replacement parks and wording cannot produce a second summary that day
	}
	if g.Kind == sigPRConflict || g.Kind == sigDraftPR || g.Kind == sigPREvent || g.Kind == sigPRMerged || g.Kind == sigNeedsUserExternalInfo {
		// Keep every task/PR/head and needs_user external-info signal independently across sweeps and restarts.
		key, _ := json.Marshal([]string{g.Todo, g.Kind, g.Key})
		sum := sha256.Sum256(key)
		return string(key), hex.EncodeToString(sum[:])
	}
	if g.Kind == sigInbox && g.Inbox != nil && g.Inbox.ToAgent == "unhandled" {
		// An unhandled source is not new when its recipient or last reply changes.
		key, _ := json.Marshal([]string{g.Todo, g.Kind, g.Key})
		sum := sha256.Sum256(key)
		return string(key), hex.EncodeToString(sum[:])
	}
	if g.externalPRReview() {
		// Keep the latest source wake per task, independent of its Agent's answer or a manager restart.
		key, _ := json.Marshal([]string{g.Todo, g.Kind, "external_pr"})
		sum := sha256.Sum256([]byte(g.Key))
		return string(key), hex.EncodeToString(sum[:])
	}
	// State timestamps and elapsed minutes deliberately do not participate: a new
	// turn or a periodic sweep is not new information when the last summary is unchanged.
	parts := []string{g.Todo, g.Machine, e.Machine, g.Session, e.AgentID, g.Kind, g.Report}
	if g.Kind == sigAllStopped {
		parts = []string{g.Todo, g.Kind} // one latest group key per task and destination, not per member
	}
	if g.To != "" { // a card's rung (ask_route.go): each conversation hears of it; the others' keys stay as they were
		parts = append(parts, g.To)
	}
	key, _ := json.Marshal(parts)
	text := e.Last
	if text == "" {
		text = g.Detail
	}
	fields := []any{strings.Join(strings.Fields(text), " "), strings.Join(strings.Fields(e.Ask), " "), e.Said, e.Evidence, e.Step, e.Steps}
	if g.Kind == sigDecide {
		fields = append(fields, strings.Join(strings.Fields(e.Recommend), " "))
	}
	if len(e.Asks) > 0 { // the cards it lists are what is new about it
		fields = append(fields, e.Asks)
	}
	if keyIsNews(g.Kind) {
		fields = append(fields, g.Key)
	}
	if g.Kind == sigAllStopped {
		fields = []any{g.Key} // reworded triage on the same stopped turn is not a new period of inactivity
	}
	payload, _ := json.Marshal(fields)
	sum := sha256.Sum256(payload)
	return string(key), hex.EncodeToString(sum[:])
}

// keyIsNews: a signal of kind whose key names one event that is not in its agent's words, so a new key is news
// whatever the agent said last: each park's time, each decision that ended a park, each new Inbox message, each
// answer the owner gives a decision's round, each recheck or undecidable review of a decision, each start attempt's
// failure or start (#6027). The other kinds' keys move with every turn or sweep, which is not news while the agent's
// words stay the same.
func keyIsNews(kind string) bool {
	switch kind {
	case sigParkedDue, sigParkDecided, sigInbox, sigOwnerCannot, sigRecheck, sigUndecidable, sigDeliveryHold, sigFailed, sigStarted, sigAllStopped, sigDecisionReply, sigHandoffResult, sigEstimateDue, sigOverdue, sigTimedOut, sigCardlessWait, sigTodoReady, sigPRMerged, sigParentCloseout, sigNeedsUserExternalInfo,
		sigCheck: // a check's run, or the owner's answer to its decision: an Agent that only runs tools says the same all along
		return true
	}
	return false
}

// keptUntilChange: a routine signal whose fingerprint (the agent's last words, its cards, its report, its step) is the
// whole of what the supervisor needs to know about it. The same fingerprint is not told again while the task is open
// (#11614): before, it woke the supervisor again every superviseDedupWindow although nothing had changed. A new turn
// with other words, a new card or a new report changes the fingerprint and is told as before. Signals that wait on a
// decision keep the short window or their own news key (keyIsNews).
func keptUntilChange(g superviseSignal) bool {
	switch g.Kind {
	case sigReport:
		return g.Report != upReportQuestion && g.Report != upReportBlocked
	case sigIdle, sigUnjudged, sigStalled, sigNoProgress, sigReview, sigOrphaned, sigInterrupted, sigOverdue, sigTimedOut:
		return true
	}
	return false
}

type supervisionDelivery int

const (
	digestDelivery supervisionDelivery = iota
	leaderDelivery
	leaderResumeDelivery
)

// reserveSupervision checkpoints deduplication before dispatch. A failed dispatch
// and queue admission releases the reservation. dedupMu is held by sendDigest.
func (s *Service) reserveSupervision(ctx context.Context, entries []prompts.SuperviseEntry, signals []superviseSignal, now time.Time) ([]prompts.SuperviseEntry, []superviseSignal, func(), error) {
	return s.reserveSupervisionDelivery(ctx, entries, signals, now, digestDelivery)
}

// Offline resumes take minutes. Keep their reservations in memory until the send
// is confirmed so recovery never mistakes an in-flight resume for delivery.
func (s *Service) reserveSupervisionDelivery(ctx context.Context, entries []prompts.SuperviseEntry, signals []superviseSignal, now time.Time, delivery supervisionDelivery) ([]prompts.SuperviseEntry, []superviseSignal, func(), error) {
	b := &s.supervise
	history := b.dedup
	if st := s.store(); st != nil {
		if _, err := st.Setting(ctx, superviseDedupKey, &history); err != nil {
			return nil, signals, nil, err
		}
	}
	if history == nil {
		history = map[string]superviseFingerprint{}
	}
	previous := maps.Clone(history)
	next := maps.Clone(history)
	payloadHistory := b.payloads
	if st := s.store(); st != nil {
		if _, err := st.Setting(ctx, supervisePayloadsKey, &payloadHistory); err != nil {
			return nil, signals, nil, err
		}
	}
	if payloadHistory == nil {
		payloadHistory = map[string]superviseFingerprint{}
	}
	previousPayloads := maps.Clone(payloadHistory)
	nextPayloads := maps.Clone(payloadHistory)
	for key, seen := range next {
		if !seen.Permanent && seen.Todo == "" && now.Sub(seen.At) >= superviseDedupWindow {
			delete(next, key)
		}
	}
	var out []prompts.SuperviseEntry
	var kept []superviseSignal
	// Read the outbox once for the whole batch. Only real delivery or queue admission acknowledges a stretch. An exact
	// outbox member under the fingerprint's Key may have crashed after reservation. A different Key retries only while
	// that fingerprint's stretch is still one of its unacknowledged predecessors; a real predecessor ACK removes it.
	r := &s.relays
	r.mu.Lock()
	if !s.relayLocked(ctx) {
		r.mu.Unlock()
		return nil, signals, nil, errors.New("load supervision outbox")
	}
	pending := maps.Clone(r.st.SupervisePending)
	r.mu.Unlock()
	var resumingIDs [][2]string
	resuming := delivery == leaderResumeDelivery
	for i, entry := range entries {
		key, hash := digestFingerprint(entry, signals[i])
		current, undelivered := pending[signals[i].id()]
		if seen, ok := next[key]; ok && seen.Hash == hash && (seen.Permanent || seen.Todo != "" || now.Sub(seen.At) < superviseDedupWindow) {
			if seen.Permanent && seen.Key == "" {
				continue // a delivered PR head is told once for good; a reserved Key still owes a delivery
			}
			retry := undelivered && current.Key == signals[i].Key &&
				(seen.Key == signals[i].Key || slices.Contains(current.PendingPredecessors, seen.Key))
			if !retry {
				continue
			}
		}
		id := [2]string{key, hash}
		if _, ok := b.resuming[id]; ok && delivery != digestDelivery {
			continue
		}
		if resuming {
			if b.resuming == nil {
				b.resuming = make(map[[2]string]struct{})
			}
			b.resuming[id] = struct{}{}
			resumingIDs = append(resumingIDs, id)
		}
		fingerprint := superviseFingerprint{Hash: hash, At: now.UTC(), Key: signals[i].Key, Permanent: signals[i].Kind == sigPRConflict ||
			signals[i].Kind == sigDraftPR || signals[i].Kind == sigParkedSummary || signals[i].Kind == sigPREvent || signals[i].Kind == sigPRMerged || signals[i].Kind == sigNeedsUserExternalInfo}
		if signals[i].Kind == sigAllStopped || signals[i].Kind == sigParkedSummary || signals[i].Kind == sigPREvent || signals[i].Kind == sigPRMerged || signals[i].Kind == sigNeedsUserExternalInfo || keptUntilChange(signals[i]) {
			fingerprint.Todo = signals[i].Todo
		}
		if g := signals[i]; g.Kind == sigTodoReady || g.Kind == sigParentCloseout || g.externalPRReview() || g.Kind == sigInbox && g.Inbox != nil && g.Inbox.ToAgent == "unhandled" {
			fingerprint.Todo = g.Todo
		}
		next[key] = fingerprint

		// Reserve task prose separately from whole-signal deduplication. The full entry above decides whether the
		// signal is news; these hashes only let its final rendering omit what this recipient already has.
		g := &signals[i]
		g.RepeatCriteria, g.RepeatRequest, g.RepeatSaid, g.RepeatLast, g.RepeatEvidence = "", "", "", "", nil
		payload := currentSuperviseEventPayload(entry)
		payloadKey := superviseEventPayloadKey(*g)
		if previous := nextPayloads[payloadKey].Payload; previous != nil {
			if previous.Criteria == payload.Criteria {
				g.RepeatCriteria = payload.Criteria
			}
			if previous.Request == payload.Request {
				g.RepeatRequest = payload.Request
			}
			if payload.Said != "" && previous.Said == payload.Said {
				g.RepeatSaid = payload.Said
			}
			if entry.Last != "" && previous.Last == payload.Last {
				g.RepeatLast = payload.Last
			}
			g.RepeatEvidence = repeatedEvidence(previous.Evidence, payload.Evidence)
		}
		nextPayloads[payloadKey] = superviseFingerprint{At: now.UTC(), Key: g.Key, Todo: g.Todo, Payload: &payload}
		out, kept = append(out, entry), append(kept, *g)
	}
	if resuming {
		return out, kept, func() {
			for _, id := range resumingIDs {
				delete(b.resuming, id)
			}
		}, nil
	}
	if len(out) == 0 {
		return nil, nil, func() {}, nil
	}
	if err := s.putSupervisionHistories(ctx, next, nextPayloads); err != nil {
		if s.store() != nil {
			if restoreErr := s.putSupervisionHistories(ctx, previous, previousPayloads); restoreErr != nil {
				s.logf("supervise: could not roll back paired deduplication histories: %v", restoreErr)
			}
		}
		return nil, signals, nil, err
	}
	b.dedup, b.payloads = next, nextPayloads
	return out, kept, func() {
		b.dedup, b.payloads = previous, previousPayloads
		if s.store() != nil {
			cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := s.putSupervisionHistories(cctx, previous, previousPayloads); err != nil {
				s.logf("supervise: could not release deduplication reservation: %v", err)
			}
		}
	}, nil
}

// ackSupervisionDedupLocked marks stable per-head and per-source reservations as delivered. An empty Key distinguishes
// a delivered notification from one whose process stopped after reservation but before delivery or queue admission
// (#5732). Other fingerprints depend on their rendered entry and are not recomputed here. dedupMu is held by sendDigest.
func (s *Service) ackSupervisionDedupLocked(ctx context.Context, signals []superviseSignal) {
	b := &s.supervise
	history := b.dedup
	st := s.store()
	if st != nil {
		if _, err := st.Setting(ctx, superviseDedupKey, &history); err != nil {
			s.logf("supervise: could not read deduplication history for delivery acknowledgment: %v", err)
			return
		}
	}
	next := maps.Clone(history)
	changed := false
	for _, g := range signals {
		stable := g.Kind == sigPRConflict || g.Kind == sigDraftPR || g.Kind == sigPRMerged || g.Kind == sigNeedsUserExternalInfo ||
			g.Kind == sigInbox && g.Inbox != nil && g.Inbox.ToAgent == "unhandled"
		if !stable {
			continue
		}
		key, _ := digestFingerprint(prompts.SuperviseEntry{}, g)
		if seen, ok := next[key]; ok && seen.Key != "" && seen.Key == g.Key {
			seen.Key = ""
			next[key] = seen
			changed = true
		}
	}
	if !changed {
		return
	}
	if st != nil {
		if err := st.PutSetting(ctx, superviseDedupKey, next); err != nil {
			// The message already went out. Keep the in-memory ACK even when its durable copy fails; a later restart may
			// retry the head, which is safer than rolling back an actual delivery and losing the obligation instead.
			s.logf("supervise: could not persist delivery acknowledgment: %v", err)
		}
	}
	b.dedup = next
}

// forgetSupervision takes the signals' fingerprints out of the deduplication history: their message was queued for a
// conversation that is gone and was never read, so they are told again. dedupMu is held.
func (s *Service) forgetSupervision(ctx context.Context, signals []superviseSignal, web string) {
	b := &s.supervise
	history := b.dedup
	st := s.store()
	if st != nil {
		if _, err := st.Setting(ctx, superviseDedupKey, &history); err != nil {
			s.logf("supervise: could not read the deduplication history: %v", err)
			return
		}
	}
	payloadHistory := b.payloads
	if st != nil {
		if _, err := st.Setting(ctx, supervisePayloadsKey, &payloadHistory); err != nil {
			s.logf("supervise: could not read the payload history: %v", err)
			return
		}
	}
	next := maps.Clone(history)
	nextPayloads := maps.Clone(payloadHistory)
	payloadChanged := false
	for _, g := range signals {
		payloadKey := superviseEventPayloadKey(g)
		if seen := nextPayloads[payloadKey]; seen.Key == g.Key {
			delete(nextPayloads, payloadKey) // a lost queue must not make its first later delivery look repeated
			payloadChanged = true
		}
		if g.Kind == sigAllStopped {
			key, _ := digestFingerprint(prompts.SuperviseEntry{}, g)
			delete(next, key) // a lost queue releases its key even while the participants cannot be read
			continue
		}
		if e, ok := s.digestEntry(ctx, g, web); ok {
			key, _ := digestFingerprint(e, g)
			delete(next, key)
		}
	}
	if len(next) == len(history) && !payloadChanged {
		return
	}
	if st != nil {
		if err := s.putSupervisionHistories(ctx, next, nextPayloads); err != nil {
			s.logf("supervise: could not release deduplication entries: %v", err)
			return
		}
	}
	b.dedup, b.payloads = next, nextPayloads
}

// Task-bound fingerprints are bounded by open tasks, not elapsed time. Load once
// after restart; reservations keep the same cache current under dedupMu.
func (s *Service) pruneAllStoppedDedup(ctx context.Context) {
	b := &s.supervise
	b.dedupMu.Lock()
	defer b.dedupMu.Unlock()
	if b.dedup == nil {
		history := map[string]superviseFingerprint{}
		if st := s.store(); st != nil {
			if _, err := st.Setting(ctx, superviseDedupKey, &history); err != nil {
				s.logf("supervise: cannot load inactivity fingerprints: %v", err)
				return
			}
		}
		b.dedup = history
	}
	if b.payloads == nil {
		history := map[string]superviseFingerprint{}
		if st := s.store(); st != nil {
			if _, err := st.Setting(ctx, supervisePayloadsKey, &history); err != nil {
				s.logf("supervise: cannot load payload history: %v", err)
				return
			}
		}
		b.payloads = history
	}
	var remove, payloadRemove []string
	s.todos.mu.RLock()
	for key, seen := range b.dedup {
		todo := seen.Todo
		if todo == "" && seen.Permanent {
			// Older daily summaries omitted Todo; only their exact key shape supplies the missing binding.
			var parts []string
			if json.Unmarshal([]byte(key), &parts) == nil && len(parts) == 3 && parts[1] == sigParkedSummary {
				todo = parts[0]
			}
		}
		if task := s.todos.todos[todo]; todo != "" && s.todos.loaded && (task == nil || task.DoneAt != nil) {
			remove = append(remove, key)
		}
	}
	for key, seen := range b.payloads {
		if task := s.todos.todos[seen.Todo]; seen.Todo != "" && s.todos.loaded && (task == nil || task.DoneAt != nil) {
			payloadRemove = append(payloadRemove, key)
		}
	}
	s.todos.mu.RUnlock()
	if len(remove) == 0 && len(payloadRemove) == 0 {
		return
	}
	next := maps.Clone(b.dedup)
	for _, key := range remove {
		delete(next, key)
	}
	nextPayloads := maps.Clone(b.payloads)
	for _, key := range payloadRemove {
		delete(nextPayloads, key)
	}
	if st := s.store(); st != nil {
		if err := s.putSupervisionHistories(ctx, next, nextPayloads); err != nil {
			s.logf("supervise: cannot prune supervision histories: %v", err)
			return
		}
	}
	b.dedup, b.payloads = next, nextPayloads
}

// clearTodoReadyDedup removes the task-bound sigTodoReady deduplication entry for todoID when that to-do is reopened,
// so a subsequent readiness transition after reopen is not suppressed by the earlier wake.
func (s *Service) clearTodoReadyDedup(ctx context.Context, todoID string) {
	s.clearTaskBoundSignalDedup(ctx, todoID, sigTodoReady)
}

// clearParentCloseoutDedup removes the task-bound sigParentCloseout deduplication entry for todoID when reopened.
func (s *Service) clearParentCloseoutDedup(ctx context.Context, todoID string) {
	s.clearTaskBoundSignalDedup(ctx, todoID, sigParentCloseout)
}

func (s *Service) clearTaskBoundSignalDedup(ctx context.Context, todoID, kind string) {
	b := &s.supervise
	b.dedupMu.Lock()
	defer b.dedupMu.Unlock()
	history := b.dedup
	if st := s.store(); st != nil {
		loaded := map[string]superviseFingerprint{}
		if _, err := st.Setting(ctx, superviseDedupKey, &loaded); err != nil {
			s.logf("supervise: cannot load deduplication history for %s reset (%s): %v", kind, todoID, err)
			return
		}
		history = loaded
	}
	if len(history) == 0 {
		return
	}
	key, _ := digestFingerprint(prompts.SuperviseEntry{}, superviseSignal{Todo: todoID, Kind: kind})
	if _, ok := history[key]; !ok {
		return
	}
	next := maps.Clone(history)
	delete(next, key)
	if st := s.store(); st != nil {
		if err := st.PutSetting(ctx, superviseDedupKey, next); err != nil {
			s.logf("supervise: cannot clear %s deduplication entry for %s: %v", kind, todoID, err)
			return
		}
	}
	b.dedup = next
}

func (s *Service) restoreDigest(signals []superviseSignal, at, prev time.Time) {
	b := &s.supervise
	b.mu.Lock()
	b.init()
	defer b.mu.Unlock()
	if b.last.Equal(at) {
		b.last = prev
	}
	for _, g := range signals {
		if b.sent[g.id()] == g.Key {
			delete(b.sent, g.id())
		}
		if cur, newer := b.pending[g.id()]; !newer {
			b.pending[g.id()] = g
		} else if g.Kind == sigInbox && cur.Key != g.Key {
			b.pending[g.id()] = joinInbox(g, cur) // the newer item, with the messages of both
		}
	}
	s.updateSupervisePending(func(pending map[string]superviseSignal) {
		for _, g := range signals {
			if cur, ok := b.pending[g.id()]; ok {
				pending[g.id()] = cur
			}
		}
	})
	s.retryDigestLocked(s.superviseTimings().urgent)
}
