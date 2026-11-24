package assistant

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/kb"
)

func larkcliThing(chatID, kind, title, last, text string, at time.Time) connector.InboxThing {
	return connector.InboxThing{
		Thread:  "larkcli:chat:" + chatID,
		Kind:    kind,
		Title:   title,
		Sender:  "Peer",
		Preview: text,
		Context: "[2030-01-01T00:00:00Z] Peer (id=" + last + "): " + text,
		At:      at,
		Last:    last,
		Tags:    []connector.Tag{{Key: connector.TagChat, Value: chatID, Name: title}},
	}
}

// Synthetic direct chats and explicitly excluded groups must never reach triage, model context,
// KB context, folders, or suggestions, while ordinary allowed groups continue working normally.
func TestInboxVisibilityExcludesDMsAndExcludedGroupsFromAI(t *testing.T) {
	h := newInboxHub(t)
	model, calls := captureModel(t, `{"tier":"action","summary":"Needs review","reason":"direct question"}`)
	h.in.setModel(model)

	h.setConfig(func(c *assistantwire.InboxConfig) {
		c.Lark = assistantwire.InboxVisibility{
			ExcludeDirectChats: true,
			ExcludedChatIDs:    []string{"oc_example_excluded_group"},
		}
	})

	now := time.Now().UTC()
	round := assistantwire.InboxRound{
		Epoch: "e1",
		Seq:   1,
		Round: assistantwire.RoundPoll,
		Names: map[string]string{"larkcli": "Lark (as you)"},
		Sources: []assistantwire.InboxSource{{
			ID:   "larkcli",
			Name: "Lark (as you)",
			Things: []connector.InboxThing{
				larkcliThing("oc_example_dm", KindDM, "Private Peer", "om_dm_1", "Private DM secret", now),
				larkcliThing("oc_example_bot", KindBot, "Private Bot", "om_bot_1", "Bot DM secret", now),
				larkcliThing("oc_example_excluded_group", KindMention, "Excluded Group", "om_excl_1", "Excluded group secret", now),
				larkcliThing("oc_example_allowed_group", KindMention, "Allowed Group", "om_ok_1", "Allowed group question", now),
			},
		}},
	}
	if err := h.InboxCollected(t.Context(), round); err != nil {
		t.Fatalf("InboxCollected: %v", err)
	}
	// Wait for background triage pass to finish.
	h.inbox.triaging.Lock()
	h.inbox.triaging.Unlock()

	h.inbox.mu.Lock()
	if len(h.inbox.items) != 1 {
		t.Fatalf("expected only 1 allowed group item, got %d: %+v", len(h.inbox.items), h.inbox.items)
	}
	var only *InboxItem
	for _, it := range h.inbox.items {
		only = it
	}
	h.inbox.mu.Unlock()

	if only.Title != "Allowed Group" || only.Tier != TierAction {
		t.Fatalf("allowed group item was not triaged normally: %+v", only)
	}
	gotCalls := calls()
	if len(gotCalls) != 1 {
		t.Fatalf("expected exactly 1 triage model call for allowed group, got %d", len(gotCalls))
	}
	for _, c := range gotCalls {
		for _, forbidden := range []string{"Private DM secret", "Bot DM secret", "Excluded group secret"} {
			if strings.Contains(c.User, forbidden) || strings.Contains(c.System, forbidden) {
				t.Fatalf("excluded content %q reached model prompt: %+v", forbidden, c)
			}
		}
	}
}

// source_only disables automatic KB injection, rejects KB/unrestricted-search/cross-conversation reads,
// and validates message-read targets before downstream calls so direct-chat reads make zero downstream calls.
func TestInboxSourceOnlyReadProfileEnforcesToolAndContextBoundaries(t *testing.T) {
	h := newInboxHubWith(t, &KBOptions{Dir: t.TempDir()})
	h.setConfig(func(c *assistantwire.InboxConfig) {
		c.ReadProfile = assistantwire.InboxReadProfileSourceOnly
		c.Agent.Enabled = true
		c.Agent.Machine = "server"
		c.Agent.Concurrency = 2
	})

	// Seed a KB file for the group chat to verify automatic KB injection is skipped under source_only.
	chatDir := h.kb.dir + "/chats/" + kb.ChatID("lark", "oc_example_group")
	mustWriteFile(t, chatDir+"/summary.md", "Confidential KB memory that must not be injected")

	now := time.Now().UTC()
	groupItem := &InboxItem{
		ID:        "larkcli:oc_example_group:1",
		Source:    "larkcli",
		Kind:      KindMention,
		Title:     "Engineering Group",
		Preview:   "Can you check message om_ok_1?",
		Context:   "[2030-01-01T00:00:00Z] Teammate (id=om_ok_1): Can you check this?",
		Last:      "om_ok_1",
		TriagedAt: "om_ok_1",
		Tier:      TierAction,
		StartedAt: now,
		UpdatedAt: now,
		Connector: &InboxConnector{
			Thread: "larkcli:chat:oc_example_group",
			Tags:   []connector.Tag{{Key: connector.TagChat, Value: "oc_example_group", Name: "Engineering Group"}},
		},
		Agent: &InboxAgent{
			State: AgentQueued,
			Of:    "om_ok_1",
		},
	}
	dmItem := &InboxItem{
		ID:        "larkcli:oc_example_dm:1",
		Source:    "larkcli",
		Kind:      KindDM,
		Title:     "Direct Chat",
		Preview:   "Private question",
		Context:   "[2030-01-01T00:00:00Z] Peer (id=om_dm_1): Private question",
		Last:      "om_dm_1",
		TriagedAt: "om_dm_1",
		Tier:      TierAction,
		StartedAt: now,
		UpdatedAt: now,
		Connector: &InboxConnector{
			Thread: "larkcli:chat:oc_example_dm",
			Tags:   []connector.Tag{{Key: connector.TagChat, Value: "oc_example_dm", Name: "Direct Chat"}},
		},
		Agent: &InboxAgent{
			State:     AgentRunning,
			MachineID: "server",
			SessionID: "sess-dm",
			Of:        "om_dm_1",
		},
	}

	h.inbox.mu.Lock()
	h.inbox.items[groupItem.ID] = groupItem
	h.inbox.items[dmItem.ID] = dmItem
	h.inbox.mu.Unlock()
	h.inbox.loaded.Store(true)

	// Start the handling agent for the allowed group item and verify zero KB injection in its prompt.
	h.startInboxAgents(t.Context(), h.in.cfg.Agent)
	h.f.mu.Lock()
	sends := slices.Clone(h.f.sends)
	h.f.mu.Unlock()
	var startText string
	for _, entry := range sends {
		if text, ok := entry["text"].(string); ok && text != "" {
			startText = text
			break
		}
	}
	if startText == "" {
		t.Fatalf("expected handling chat start prompt in assistant.send, got sends=%+v", sends)
	}
	if strings.Contains(startText, "Confidential KB memory") || strings.Contains(startText, h.kb.dir) {
		t.Fatalf("source_only injected KB context or directory into handling prompt:\n%s", startText)
	}
	groupSession := groupItem.Agent.SessionID
	h.originSet(t.Context(), "server", groupSession, OriginInbox, groupItem.ID, "")
	h.originSet(t.Context(), "server", "sess-dm", OriginInbox, dmItem.ID, "")

	ext := &policyExtensions{read: map[string]bool{
		"larkcli_messages": true,
		"larkcli_chats":    true,
		"larkcli_search":   true,
		"lark_messages":    true,
		"lark_chats":       true,
		"lark_search":      true,
		"lark_image":       true,
		"mail_search":      true,
	}}
	h.SetExtensions(ext)

	// 1. KB tools and unrestricted conversation/search tools must all be rejected with zero downstream calls.
	for _, tc := range []struct {
		name string
		args string
	}{
		{"kb_search", `{"query":"secret"}`},
		{"kb_list", `{}`},
		{"kb_get", `{"path":"summary.md"}`},
		{"kb_update", `{"path":"note.md","content":"x"}`},
		{"conversations_list", `{}`},
		{"conversation_read", `{"session_id":"other"}`},
		{"inbox_folders", `{}`},
		{"larkcli_chats", `{"filter":"recent"}`},
		{"larkcli_search", `{"query":"secret"}`},
		{"lark_chats", `{"filter":"recent"}`},
		{"lark_search", `{"query":"secret"}`},
		{"mail_search", `{"query":"secret"}`},
		{"phone_calendar_events", `{}`},
	} {
		res := h.mcpCallWithOrigin(t.Context(), "server", groupSession, tc.name, json.RawMessage(tc.args), true, OriginInbox)
		if !res.IsError {
			t.Fatalf("source_only allowed %s: %+v", tc.name, res)
		}
		res2 := inboxTool(t, h, groupSession, tc.name, tc.args)
		if !res2.IsError {
			t.Fatalf("source_only Tool allowed %s: %+v", tc.name, res2)
		}
	}

	// 2. Cross-conversation reads and foreign image reads must fail closed with zero downstream calls.
	for _, tc := range []struct {
		session string
		name    string
		args    string
	}{
		{groupSession, "larkcli_messages", `{"chat_id":"oc_other_group"}`},
		{groupSession, "larkcli_messages", `{"chat_id":"oc_example_dm"}`},
		{groupSession, "larkcli_messages", `{}`},
		{groupSession, "lark_messages", `{"chat_id":"oc_other_group"}`},
		{groupSession, "lark_image", `{"message":"om_foreign_msg","chat_id":"oc_example_group"}`},
		{groupSession, "lark_image", `{"message":"om_ok_1","chat_id":"oc_other_group"}`},
		// Direct-chat message reads from a DM handling chat must fail closed with zero downstream calls.
		{"sess-dm", "larkcli_messages", `{"chat_id":"oc_example_dm"}`},
		{"sess-dm", "lark_messages", `{"chat_id":"oc_example_dm"}`},
	} {
		res := h.mcpCallWithOrigin(t.Context(), "server", tc.session, tc.name, json.RawMessage(tc.args), true, OriginInbox)
		if !res.IsError {
			t.Fatalf("source_only allowed %s(%s) on %s: %+v", tc.name, tc.args, tc.session, res)
		}
	}
	if len(ext.calls) != 0 {
		t.Fatalf("rejected source_only calls reached downstream extensions: %v", ext.calls)
	}

	// 3. Same-conversation read for the bound allowed group succeeds.
	okRes := h.mcpCallWithOrigin(t.Context(), "server", groupSession, "larkcli_messages", json.RawMessage(`{"chat_id":"oc_example_group"}`), true, OriginInbox)
	if okRes.IsError || toolText(okRes) != "read larkcli_messages" {
		t.Fatalf("source_only blocked bound group's own larkcli_messages read: %+v", okRes)
	}
	okImg := h.mcpCallWithOrigin(t.Context(), "server", groupSession, "lark_image", json.RawMessage(`{"message":"om_ok_1","chat_id":"oc_example_group"}`), true, OriginInbox)
	if okImg.IsError || toolText(okImg) != "read lark_image" {
		t.Fatalf("source_only blocked bound group's own lark_image read: %+v", okImg)
	}
	if len(ext.calls) != 2 {
		t.Fatalf("expected exactly 2 allowed downstream calls, got %v", ext.calls)
	}

	// 4. inbox_suggest still works for the bound allowed group.
	turn := inboxTestProcessingTurn(h, groupSession)
	suggestArgs := fmt.Sprintf(`{"processing_turn":%q,"kind":"reply","text":"Group reply draft","recap":"Ready"}`, turn)
	if res := inboxTool(t, h, groupSession, "inbox_suggest", suggestArgs); res.IsError {
		t.Fatalf("source_only blocked inbox_suggest for allowed group: %+v", res)
	}
}

// Existing queued, handling, and suggested excluded items must be quarantined out of AI handling
// while preserving history (Chain, DoneAt/DoneBy) and sent-attempt audit records (SuggestionAttempts),
// and suggestion/confirmation/handoff/draft/retry boundaries must fail closed.
func TestInboxQuarantineExistingExcludedItemsPreservesAuditAndFailsClosed(t *testing.T) {
	h := newInboxHub(t)
	model, calls := captureModel(t, `{"tier":"action","summary":"Should not run","reason":"test"}`)
	h.in.setModel(model)

	var outboundReplies int
	h.f.answerApp(AppInbox, inboxReplyMethod, func(json.RawMessage) (any, error) {
		outboundReplies++
		return assistantwire.InboxSent{}, nil
	})

	now := time.Now().UTC()
	checkAt := now.Add(-time.Minute)
	sentAt := now.Add(-10 * time.Minute)
	auditAttempt := &InboxSuggestionAttempt{
		Version:  1,
		Status:   suggestionSent,
		Text:     "Earlier confirmed reply",
		At:       sentAt,
		ClientID: "send-audit-1",
		Target:   "target-1",
	}

	queuedDM := &InboxItem{
		ID:        "larkcli:oc_dm_queued:1",
		Source:    "larkcli",
		Kind:      KindDM,
		Title:     "Queued DM",
		Preview:   "Hello",
		Context:   "[2030-01-01T00:00:00Z] Peer: Hello",
		Last:      "om_q1",
		TriagedAt: "om_q1",
		Tier:      TierAction,
		StartedAt: now,
		UpdatedAt: now,
		Connector: &InboxConnector{Thread: "larkcli:chat:oc_dm_queued"},
		Agent:     &InboxAgent{State: AgentQueued, Of: "om_q1", TodoID: "todo-1"},
		Chain:     []InboxChainEntry{{ID: "c1", Kind: ChainReceived, At: now, Text: "Received"}},
	}
	handlingDM := &InboxItem{
		ID:        "larkcli:oc_dm_handling:1",
		Source:    "larkcli",
		Kind:      KindDM,
		Title:     "Handling DM",
		Preview:   "Still waiting",
		Context:   "[2030-01-01T00:00:00Z] Peer: Still waiting",
		Last:      "om_h1",
		TriagedAt: "om_h1",
		Tier:      TierAction,
		StartedAt: now,
		UpdatedAt: now,
		Connector: &InboxConnector{Thread: "larkcli:chat:oc_dm_handling"},
		Handling:  &InboxHandling{Owner: HandlingOwner, State: HandlingLooking, MachineID: "server", SessionID: "sess-h1"},
		Follow:    &InboxFollow{Who: "Peer", Next: "Check reply", CheckAt: &checkAt},
		Agent: &InboxAgent{
			State:          AgentRunning,
			MachineID:      "server",
			SessionID:      "sess-h1",
			Of:             "om_h1",
			ProcessingTurn: "turn-h1",
			Wake:           true,
			Pending:        []inboxNote{{Name: "new_messages", Context: "hi"}},
		},
		Chain: []InboxChainEntry{{ID: "c1", Kind: ChainReceived, At: now, Text: "Received"}},
	}
	suggestedGroup := &InboxItem{
		ID:                 "lark:oc_excl_group:1",
		Source:             "lark",
		Kind:               KindMention,
		Title:              "Excluded Group",
		Preview:            "Need confirmation",
		Context:            "[2030-01-01T00:00:00Z] Peer: Need confirmation",
		Last:               "100",
		TriagedAt:          "100",
		Tier:               TierAction,
		StartedAt:          now,
		UpdatedAt:          now,
		Lark:               &InboxLark{FeedID: "oc_excl_group", IsGroup: true, ReplyTo: "100"},
		Handling:           &InboxHandling{Owner: HandlingOwner, State: HandlingDone, MachineID: "server", SessionID: "sess-s1"},
		Suggestion:         &InboxSuggestion{Version: 2, Kind: SuggestReply, Text: "Proposed reply", At: now},
		SuggestionAttempts: []*InboxSuggestionAttempt{auditAttempt},
		Handoffs:           []InboxHandoff{{MachineID: "server", SessionID: "main-chat", At: now, PendingSuggestion: true}},
		Agent: &InboxAgent{
			State:          AgentDrafted,
			MachineID:      "server",
			SessionID:      "sess-s1",
			Draft:          "Proposed reply",
			Of:             "100",
			ProcessingTurn: "turn-s1",
		},
		Chain: []InboxChainEntry{
			{ID: "c1", Kind: ChainReceived, At: sentAt, Text: "Received"},
			{ID: "c2", Kind: ChainSent, At: sentAt, Text: "Sent a Lark message"},
		},
	}

	// Persist items in the backing store BEFORE enabling exclusions, then reload through inboxReady!
	for _, it := range []*InboxItem{queuedDM, handlingDM, suggestedGroup} {
		raw, _ := json.Marshal(it)
		if err := h.st.PutInboxItem(t.Context(), it.ID, raw, it.UpdatedAt, it.DoneAt); err != nil {
			t.Fatal(err)
		}
	}

	h.setConfig(func(c *assistantwire.InboxConfig) {
		c.Agent.Enabled = true
		c.Lark = assistantwire.InboxVisibility{
			ExcludeDirectChats: true,
			ExcludedChatIDs:    []string{"oc_excl_group"},
		}
	})

	// Loading via inboxReady must quarantine all 3 items and persist their quarantined state.
	if err := h.inboxReady(t.Context()); err != nil {
		t.Fatalf("inboxReady: %v", err)
	}
	// Run a handling pass as well: must not start, wake, or triage any quarantined item.
	h.handPass(t.Context())
	h.triageInbox(t.Context())
	h.inbox.triaging.Lock()
	h.inbox.triaging.Unlock()

	h.inbox.mu.Lock()
	q := h.inbox.items[queuedDM.ID]
	hd := h.inbox.items[handlingDM.ID]
	sg := h.inbox.items[suggestedGroup.ID]
	h.inbox.mu.Unlock()

	for _, it := range []*InboxItem{q, hd, sg} {
		if !it.Quarantined {
			t.Fatalf("%s was not quarantined", it.ID)
		}
		if it.Suggestion != nil || legacySuggestion(it) != nil {
			t.Fatalf("%s still has an active suggestion after quarantine: %+v / %+v", it.ID, it.Suggestion, it.Agent)
		}
		if it.Agent.State != AgentSkipped || len(it.Agent.Pending) != 0 || it.Agent.Wake || it.Agent.TodoID != "" {
			t.Fatalf("%s still has AI-eligible agent state after quarantine: %+v", it.ID, it.Agent)
		}
		if it.Handling != nil && it.Handling.State != HandlingDone {
			t.Fatalf("%s handling state = %q, want done", it.ID, it.Handling.State)
		}
		if it.Follow != nil && (it.Follow.CheckAt != nil || it.Follow.Due) {
			t.Fatalf("%s still has scheduled follow-up after quarantine: %+v", it.ID, it.Follow)
		}
	}
	// History and sent-attempt audit records MUST remain completely intact!
	if len(sg.Chain) != 2 || sg.Chain[1].Kind != ChainSent {
		t.Fatalf("chain history was modified by quarantine: %+v", sg.Chain)
	}
	if len(sg.SuggestionAttempts) != 1 || sg.SuggestionAttempts[0].Status != suggestionSent || sg.SuggestionAttempts[0].ClientID != "send-audit-1" {
		t.Fatalf("sent-attempt audit record was lost or modified: %+v", sg.SuggestionAttempts)
	}
	if len(calls()) != 0 {
		t.Fatalf("quarantined items triggered %d model calls", len(calls()))
	}
	h.f.mu.Lock()
	if len(h.f.starts) != 0 || len(h.f.sends) != 0 {
		t.Fatalf("quarantined items started/woke conversations: starts=%d sends=%d", len(h.f.starts), len(h.f.sends))
	}
	h.f.mu.Unlock()

	// Fail-closed boundaries:
	// 1. Suggestion submission (both processing conversation and handed-off conversation) must fail closed.
	if _, _, _, _, err := h.suggestFrom(t.Context(), "sess-h1", suggestIn{Kind: SuggestReply, Text: "Blocked", ProcessingTurn: "turn-h1"}); err == nil {
		t.Fatal("suggestFrom allowed submitting a suggestion for an excluded DM")
	}
	if _, _, _, _, err := h.suggestFrom(t.Context(), "main-chat", suggestIn{Item: suggestedGroup.ID, Kind: SuggestReply, Text: "Blocked"}); err == nil {
		t.Fatal("suggestFrom allowed handed-off suggestion for an excluded group")
	}
	if _, _, err := h.inboxDraft(t.Context(), "sess-h1", "Blocked", "", nil, "turn-h1"); err == nil {
		t.Fatal("inboxDraft swallowed exclusion error")
	}

	// 2. Suggestion confirmation API must fail closed even if a stale suggestion were somehow present.
	h.inbox.mu.Lock()
	sg.Suggestion = &InboxSuggestion{Version: 3, Kind: SuggestReply, Text: "Stale reply", At: now}
	h.inbox.mu.Unlock()
	code, body := inboxCall(t, h.routes(), http.MethodPost, "/api/v1/inbox/"+sg.ID+"/suggestion", `{"version":3,"action":"confirm"}`)
	if code != http.StatusConflict || body["code"] != "inbox_item_excluded" || body["delivered"] != "none" {
		t.Fatalf("POST /suggestion on excluded item: status=%d body=%+v", code, body)
	}
	if outboundReplies != 0 {
		t.Fatalf("excluded suggestion confirmation sent %d outbound replies", outboundReplies)
	}

	// 3. AI draft, handoff, and retry routes must fail closed.
	code, body = inboxCall(t, h.routes(), http.MethodPost, "/api/v1/inbox/"+sg.ID+"/draft", `{}`)
	if code != http.StatusConflict || body["code"] != "inbox_item_excluded" {
		t.Fatalf("POST /draft on excluded item: status=%d body=%+v", code, body)
	}
	code, body = inboxCall(t, h.routes(), http.MethodPost, "/api/v1/inbox/"+sg.ID+"/handoff", `{"text":"Summarize this"}`)
	if code != http.StatusConflict || body["code"] != "inbox_item_excluded" {
		t.Fatalf("POST /handoff on excluded item: status=%d body=%+v", code, body)
	}
	h.inbox.mu.Lock()
	sg.Handling = &InboxHandling{Owner: HandlingOwner, State: HandlingFailed, Error: "old error"}
	sg.Suggestion = nil
	h.inbox.mu.Unlock()
	code, body = inboxCall(t, h.routes(), http.MethodPost, "/api/v1/inbox/"+sg.ID+"/retry", `{}`)
	if code != http.StatusConflict || body["code"] != "inbox_item_excluded" {
		t.Fatalf("POST /retry on excluded item: status=%d body=%+v", code, body)
	}

	// 4. Reversibility: removing exclusions clears quarantine without losing history or audit records.
	h.setConfig(func(c *assistantwire.InboxConfig) {
		c.Lark = assistantwire.InboxVisibility{}
	})
	h.quarantineExcludedInbox(t.Context(), h.inboxConfig(t.Context()))
	h.inbox.mu.Lock()
	if sg.Quarantined {
		t.Fatal("quarantine flag was not cleared when exclusion was removed")
	}
	if len(sg.SuggestionAttempts) != 1 || len(sg.Chain) != 2 {
		t.Fatalf("history or audit records lost after unquarantine: attempts=%d chain=%d", len(sg.SuggestionAttempts), len(sg.Chain))
	}
	h.inbox.mu.Unlock()
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
