package assistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"slices"
	"time"

	"github.com/nwverse/persona/server/internal/prompts"
)

const supervisePayloadsKey = "assistant.supervision_payloads"

type superviseEventPayload struct {
	Criteria, Request, Said, Last string
	Evidence                      []string
}

type supervisionPayloadRecord struct {
	At      time.Time             `json:"at"`
	Key     string                `json:"key,omitempty"`
	Todo    string                `json:"todo"`
	Payload superviseEventPayload `json:"payload"`
}

func superviseEventPayloadKey(to, todo string) string {
	key, _ := json.Marshal([]string{"event-payload", to, todo})
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

func mergeSupervisionEventPayload(previous, current superviseEventPayload) superviseEventPayload {
	// The last entry in the rendered order supplies scalar versions; every evidence item in the message counts as sent.
	out := current
	out.Evidence = make([]string, 0, len(previous.Evidence)+len(current.Evidence))
	seen := make(map[string]bool, len(previous.Evidence)+len(current.Evidence))
	for _, hashes := range [][]string{previous.Evidence, current.Evidence} {
		for _, hash := range hashes {
			if !seen[hash] {
				seen[hash] = true
				out.Evidence = append(out.Evidence, hash)
			}
		}
	}
	return out
}

func encodeSupervisionPayloads(entries []prompts.SuperviseEntry) string {
	if len(entries) == 0 {
		return ""
	}
	all := map[string]superviseEventPayload{}
	for _, entry := range entries {
		current := currentSuperviseEventPayload(entry)
		if previous, ok := all[entry.ID]; ok {
			current = mergeSupervisionEventPayload(previous, current)
		}
		all[entry.ID] = current
	}
	raw, _ := json.Marshal(all)
	return string(raw)
}

// loadSupervisionPayloads reads the last acknowledged, not merely reserved, recipient/task prose. payloadMu is held.
func (s *Service) loadSupervisionPayloads(ctx context.Context) (map[string]supervisionPayloadRecord, error) {
	history := s.supervise.payloads
	if st := s.store(); st != nil {
		if _, err := st.Setting(ctx, supervisePayloadsKey, &history); err != nil {
			return nil, err
		}
	}
	if history == nil {
		history = map[string]supervisionPayloadRecord{}
	}
	s.supervise.payloads = history
	return history, nil
}

// renderSupervisionDelivery deltas only against actual earlier sends and other entries in this final batch. The
// returned payload record travels with the message until DeviceAssistantSend succeeds; an unsent reservation changes nothing.
func (s *Service) renderSupervisionDelivery(ctx context.Context, to string, list []prompts.SuperviseEntry, originalFresh ...bool) (string, string) {
	list = sortEntries(slices.Clone(list))
	payloads := encodeSupervisionPayloads(list)
	b := &s.supervise
	b.payloadMu.Lock()
	history, err := s.loadSupervisionPayloads(ctx)
	if err != nil {
		b.payloadMu.Unlock()
		return s.renderQueuedDigest(ctx, to, list, originalFresh...), payloads
	}
	working := maps.Clone(history)
	for i := range list {
		key := superviseEventPayloadKey(to, list[i].ID)
		current := currentSuperviseEventPayload(list[i])
		if previous := working[key].Payload; previous.Criteria != "" || previous.Request != "" || previous.Said != "" || previous.Last != "" || len(previous.Evidence) > 0 {
			if previous.Criteria == current.Criteria {
				list[i].OmitCriteria = true
			}
			if previous.Request == current.Request {
				list[i].OmitRequest = true
			}
			if list[i].Last != "" && previous.Last == current.Last {
				list[i].OmitLast = true
			}
			if current.Said != "" && previous.Said == current.Said {
				list[i].Said = ""
			}
			repeated := repeatedEvidence(previous.Evidence, current.Evidence)
			if len(repeated) > 0 {
				seen := make(map[string]bool, len(repeated))
				for _, hash := range repeated {
					seen[hash] = true
				}
				filtered := make([]string, 0, len(list[i].Evidence))
				for _, evidence := range list[i].Evidence {
					if !seen[superviseContentHash(evidence)] {
						filtered = append(filtered, evidence)
					}
				}
				list[i].Evidence = filtered
			}
		}
		working[key] = supervisionPayloadRecord{Todo: list[i].ID, Payload: mergeSupervisionEventPayload(working[key].Payload, current)}
	}
	b.payloadMu.Unlock()
	return s.renderQueuedDigest(ctx, to, list, originalFresh...), payloads
}

// ackSupervisionPayload records only text accepted by DeviceAssistantSend. A failed durable copy keeps the in-memory
// acknowledgement and fails open after a restart; it never turns a queued or refused message into a delivered one.
func (s *Service) ackSupervisionPayload(ctx context.Context, to string, msg relayMsg) {
	if msg.Tool != superviseTool || msg.Payloads == "" {
		return
	}
	var delivered map[string]superviseEventPayload
	if err := json.Unmarshal([]byte(msg.Payloads), &delivered); err != nil {
		s.logf("supervise: cannot decode delivered payload record: %v", err)
		return
	}
	b := &s.supervise
	b.payloadMu.Lock()
	defer b.payloadMu.Unlock()
	history, err := s.loadSupervisionPayloads(ctx)
	if err != nil {
		s.logf("supervise: cannot read payload history for delivery acknowledgment: %v", err)
		return
	}
	next := maps.Clone(history)
	for todo, current := range delivered {
		key := superviseEventPayloadKey(to, todo)
		next[key] = supervisionPayloadRecord{At: time.Now().UTC(), Key: relayMsgID(to, msg), Todo: todo,
			Payload: mergeSupervisionEventPayload(next[key].Payload, current)}
	}
	b.payloads = next
	if st := s.store(); st != nil {
		if err := st.PutSetting(ctx, supervisePayloadsKey, next); err != nil {
			s.logf("supervise: could not persist payload delivery acknowledgment: %v", err)
		}
	}
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

// pruneSupervisionPayloads bounds acknowledged prose by the task's lifetime, independently of whole-signal dedup.
func (s *Service) pruneSupervisionPayloads(ctx context.Context) {
	b := &s.supervise
	b.payloadMu.Lock()
	defer b.payloadMu.Unlock()
	history, err := s.loadSupervisionPayloads(ctx)
	if err != nil {
		s.logf("supervise: cannot load payload history for pruning: %v", err)
		return
	}
	next := maps.Clone(history)
	s.todos.mu.RLock()
	for key, seen := range history {
		if task := s.todos.todos[seen.Todo]; seen.Todo != "" && s.todos.loaded && (task == nil || task.DoneAt != nil) {
			delete(next, key)
		}
	}
	s.todos.mu.RUnlock()
	if len(next) == len(history) {
		return
	}
	if st := s.store(); st != nil {
		if err := st.PutSetting(ctx, supervisePayloadsKey, next); err != nil {
			s.logf("supervise: cannot prune payload history: %v", err)
			return
		}
	}
	b.payloads = next
}
