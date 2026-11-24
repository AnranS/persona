package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/i18n"
)

var (
	errInboxExcluded   = i18n.ErrEnglish("inbox-errors.item_excluded")
	errSuggestExcluded = errSuggestion{"this inbox item is excluded from AI handling by Lark Inbox visibility settings; nothing was written; end this turn"}
)

// parseLarkThreadTarget extracts the chat ID and optional topic thread ID from a Lark or larkcli thread identifier.
func parseLarkThreadTarget(thread string) (chatID, threadID string, ok bool) {
	thread = strings.TrimSpace(thread)
	if rest, cut := strings.CutPrefix(thread, "larkcli:chat:"); cut {
		chat, topic, hasTopic := strings.Cut(rest, ":thread:")
		chat = strings.TrimSpace(chat)
		topic = strings.TrimSpace(topic)
		if chat == "" || (hasTopic && topic == "") {
			return "", "", false
		}
		return chat, topic, true
	}
	if rest, cut := strings.CutPrefix(thread, "chat:"); cut {
		rest = strings.TrimSpace(rest)
		if rest == "" {
			return "", "", false
		}
		// Extension chat threads may be "chat:<feedID>" or "chat:<account>:<feedID>".
		if i := strings.LastIndex(rest, ":"); i >= 0 {
			rest = strings.TrimSpace(rest[i+1:])
		}
		if rest == "" {
			return "", "", false
		}
		return rest, "", true
	}
	return "", "", false
}

// inboxItemLarkTarget identifies whether an Inbox item is a Lark chat item and returns its chat/thread scope.
func inboxItemLarkTarget(it *InboxItem) (isLark bool, chatID, threadID string, isDirect bool, account int) {
	if it == nil || it.Doc != nil || it.Moment != nil || it.Kind == "doc" || it.Kind == KindMoment {
		return false, "", "", false, 0
	}
	switch it.Source {
	case "larkcli":
		if it.Connector != nil {
			chatID, threadID, _ = parseLarkThreadTarget(it.Connector.Thread)
			if chatID == "" {
				for _, tag := range it.Connector.Tags {
					if tag.Key == connector.TagChat && strings.TrimSpace(tag.Value) != "" {
						chatID = strings.TrimSpace(tag.Value)
						break
					}
				}
			}
		}
		isDirect = it.Kind == KindDM || it.Kind == KindBot
		return true, chatID, threadID, isDirect, 0
	case "lark":
		if it.Lark != nil {
			chatID = strings.TrimSpace(it.Lark.FeedID)
			isDirect = !it.Lark.IsGroup || it.Kind == KindDM || it.Kind == KindBot
			account = larkItemAccount(it)
			return true, chatID, "", isDirect, account
		}
		if it.Group != nil && it.Group.Connector == "lark" {
			chatID, threadID, _ = parseLarkThreadTarget(it.Group.Thread)
		}
		if chatID == "" && it.Connector != nil {
			chatID, threadID, _ = parseLarkThreadTarget(it.Connector.Thread)
			if chatID == "" {
				for _, tag := range it.Connector.Tags {
					if tag.Key == connector.TagChat && strings.TrimSpace(tag.Value) != "" {
						chatID = strings.TrimSpace(tag.Value)
						break
					}
				}
			}
		}
		isDirect = it.Kind == KindDM || it.Kind == KindBot
		if chatID != "" || isDirect || it.Kind == KindGroup || it.Kind == KindMention {
			return true, chatID, threadID, isDirect, 0
		}
	}
	return false, "", "", false, 0
}

// excludesThing reports whether an incoming connector thing is excluded by Lark Inbox visibility settings.
func excludesThing(vis assistantwire.InboxVisibility, source string, t connector.InboxThing) bool {
	if !vis.Active() {
		return false
	}
	effectiveSource := source
	if t.Extra != nil && t.Extra.Source != "" {
		effectiveSource = t.Extra.Source
	}
	if effectiveSource != "larkcli" && effectiveSource != "lark" {
		return false
	}
	if t.Kind == "doc" || t.Kind == KindMoment {
		return false
	}
	if t.Extra != nil && (t.Extra.Doc != nil || t.Extra.Moment != nil) {
		return false
	}
	var chatID string
	isDirect := t.Kind == KindDM || t.Kind == KindBot
	if t.Extra != nil && len(t.Extra.Lark) > 0 {
		var l InboxLark
		if json.Unmarshal(t.Extra.Lark, &l) == nil {
			chatID = strings.TrimSpace(l.FeedID)
			if !l.IsGroup {
				isDirect = true
			}
		}
	}
	if chatID == "" {
		chatID, _, _ = parseLarkThreadTarget(t.Thread)
	}
	if chatID == "" {
		for _, tag := range t.Tags {
			if tag.Key == connector.TagChat && strings.TrimSpace(tag.Value) != "" {
				chatID = strings.TrimSpace(tag.Value)
				break
			}
		}
	}
	return vis.ExcludesChat(chatID, isDirect)
}

func (b *inboxBook) getVisibility() assistantwire.InboxVisibility {
	if p := b.visibility.Load(); p != nil {
		return *p
	}
	return assistantwire.InboxVisibility{}
}

func (b *inboxBook) setVisibility(v assistantwire.InboxVisibility) {
	norm := v.Normalized()
	b.visibility.Store(&norm)
}

// isExcluded reports whether it (or any row merged into it) is excluded under the active visibility policy. Under b.mu.
func (b *inboxBook) isExcluded(it *InboxItem) bool {
	return b.isExcludedWith(it, b.getVisibility())
}

func (b *inboxBook) isExcludedWith(it *InboxItem, vis assistantwire.InboxVisibility) bool {
	if it == nil || !vis.Active() {
		return false
	}
	if isLark, chatID, _, isDirect, _ := inboxItemLarkTarget(it); isLark && vis.ExcludesChat(chatID, isDirect) {
		return true
	}
	for _, ref := range it.MergedFrom {
		if m := b.items[ref.ID]; m != nil {
			if isLark, chatID, _, isDirect, _ := inboxItemLarkTarget(m); isLark && vis.ExcludesChat(chatID, isDirect) {
				return true
			}
		}
	}
	return false
}

// itemNeedsQuarantine reports whether an excluded item still holds AI-eligible state or an actionable suggestion. Under b.mu.
func itemNeedsQuarantine(it *InboxItem) bool {
	if !it.Quarantined || it.Suggestion != nil || legacySuggestion(it) != nil {
		return true
	}
	if it.Handling != nil && it.Handling.State != HandlingDone {
		return true
	}
	if it.Follow != nil && (it.Follow.CheckAt != nil || it.Follow.Due) {
		return true
	}
	for _, h := range it.Handoffs {
		if h.PendingSuggestion {
			return true
		}
	}
	if a := it.Agent; a != nil {
		if a.State == AgentQueued || a.State == AgentRunning || a.State == AgentDrafted ||
			a.Draft != "" || a.Note != "" || len(a.Changes) > 0 ||
			len(a.Pending) > 0 || a.Wake || a.Back != "" || a.RetryAfter != nil ||
			a.DeferredAt != nil || a.WakeSlot || a.InterruptedByDisable ||
			a.ProcessingTurn != "" || a.TodoID != "" || a.Of != it.Last {
			return true
		}
	}
	return false
}

// quarantineExcludedLocked transitions excluded items out of AI handling, suggestions, and scheduled follow-ups
// without deleting item history, Chain entries, DoneAt/DoneBy, or SuggestionAttempts audit records.
// Items that are no longer excluded have their Quarantined flag cleared. Under b.mu.
func (b *inboxBook) quarantineExcludedLocked(now time.Time) []*inboxWrite {
	var writes []*inboxWrite
	for _, it := range b.items {
		if b.isExcluded(it) {
			if !itemNeedsQuarantine(it) {
				continue
			}
			w := beginWrite(it)
			it.Quarantined = true
			it.Suggestion = nil
			for i := range it.Handoffs {
				it.Handoffs[i].PendingSuggestion = false
			}
			if it.Follow != nil {
				it.Follow.CheckAt = nil
				it.Follow.Due = false
			}
			if a := it.Agent; a != nil {
				a.Draft, a.Note, a.Changes = "", "", nil
				a.Pending, a.Wake, a.Back = nil, false, ""
				a.RetryAfter, a.DeferredAt, a.WakeSlot = nil, nil, false
				a.InterruptedByDisable = false
				a.ProcessingTurn = ""
				a.TodoID = ""
				a.Of = it.Last
				a.State = AgentSkipped
				a.Error = ""
			}
			if it.Handling != nil && it.Handling.State != HandlingDone {
				b.setHandling(it, HandlingDone, "", now)
			}
			b.settle(w)
			writes = append(writes, w)
			continue
		}
		if it.Quarantined {
			w := beginWrite(it)
			it.Quarantined = false
			b.settle(w)
			writes = append(writes, w)
		}
	}
	return writes
}

// quarantineExcludedInbox updates the active visibility policy and persists any quarantine transitions.
func (s *Service) quarantineExcludedInbox(ctx context.Context, cfg assistantwire.InboxConfig) {
	b := &s.inbox
	b.setVisibility(cfg.Lark)
	b.mu.Lock()
	writes := b.quarantineExcludedLocked(time.Now())
	b.mu.Unlock()
	if len(writes) > 0 {
		s.saveWrites(ctx, writes, "quarantined excluded items")
	}
}

// boundInboxItem resolves the Inbox item bound to a handling conversation, using either Agent.SessionID or the origin book.
func (s *Service) boundInboxItem(ctx context.Context, machine, session string) *InboxItem {
	b := &s.inbox
	b.mu.Lock()
	it := b.itemOfAgent(session)
	b.mu.Unlock()
	if it != nil {
		return it
	}
	var ref string
	s.origins.mu.Lock()
	if s.originsLocked(ctx) {
		if o := s.origins.m[session]; o != nil && o.Origin == OriginInbox && (o.MachineID == "" || s.sameMachine(machine, o.MachineID)) {
			ref = o.Ref
		}
	}
	s.origins.mu.Unlock()
	if ref == "" {
		return nil
	}
	b.mu.Lock()
	it = b.items[ref]
	b.mu.Unlock()
	return it
}

// inboxItemOwnsMessage reports whether msgID is referenced by item it's own messages. Under b.mu.
func inboxItemOwnsMessage(it *InboxItem, msgID string) bool {
	msgID = strings.TrimSpace(msgID)
	if it == nil || msgID == "" {
		return false
	}
	if strings.TrimSpace(it.Last) == msgID {
		return true
	}
	if it.Lark != nil && strings.TrimSpace(it.Lark.ReplyTo) == msgID {
		return true
	}
	if it.Context != "" {
		if strings.Contains(it.Context, "id="+msgID) ||
			strings.Contains(it.Context, "["+msgID+"]") ||
			strings.Contains(it.Context, `"`+msgID+`"`) {
			return true
		}
	}
	return false
}

// validateInboxSourceRead enforces source-scoped message/image read validation before any downstream connector call.
func (s *Service) validateInboxSourceRead(ctx context.Context, machine, session string, vis assistantwire.InboxVisibility, name string, args json.RawMessage) (string, bool) {
	it := s.boundInboxItem(ctx, machine, session)
	if it == nil {
		return "source_only profile requires a bound Inbox item before reading source messages.", false
	}
	b := &s.inbox
	b.mu.Lock()
	excluded := b.isExcludedWith(it, vis)
	isLark, itemChatID, itemThreadID, itemDirect, itemAccount := inboxItemLarkTarget(it)
	source := it.Source
	hasLarkBlock := it.Lark != nil
	ownsMsg := func(id string) bool { return inboxItemOwnsMessage(it, id) }
	b.mu.Unlock()

	if excluded {
		return "this Inbox item is excluded by Lark Inbox visibility settings.", false
	}
	if !isLark || itemChatID == "" {
		return name + " is only allowed for a bound Lark chat Inbox item under the source_only profile.", false
	}
	if itemDirect {
		return "direct-chat message reads are blocked under the source_only profile.", false
	}
	if vis.ExcludesChat(itemChatID, false) {
		return "this chat is excluded by Lark Inbox visibility settings.", false
	}
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}

	switch name {
	case "larkcli_messages", "lark_messages":
		if name == "larkcli_messages" && source != "larkcli" {
			return "larkcli_messages is only allowed for a larkcli Inbox item under the source_only profile.", false
		}
		if name == "lark_messages" && source != "lark" && source != "larkcli" {
			return "lark_messages is only allowed for a Lark Inbox item under the source_only profile.", false
		}
		var a struct {
			ChatID  string `json:"chat_id"`
			Thread  string `json:"thread"`
			Account int    `json:"account"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return "invalid arguments for " + name + " under the source_only profile.", false
		}
		chatID := strings.TrimSpace(a.ChatID)
		thread := strings.TrimSpace(a.Thread)
		if chatID == "" && thread == "" {
			return "chat_id matching the bound Inbox item is required under the source_only profile.", false
		}
		if chatID != "" && chatID != itemChatID {
			return "cross-conversation message reads are blocked under the source_only profile.", false
		}
		if itemThreadID != "" {
			if thread != itemThreadID {
				return "thread-scoped Inbox items may only read their own thread under the source_only profile.", false
			}
		} else {
			if chatID != itemChatID {
				return "chat_id must match the bound Inbox item under the source_only profile.", false
			}
		}
		if hasLarkBlock && a.Account != 0 && a.Account != itemAccount {
			return "cross-account message reads are blocked under the source_only profile.", false
		}
		return "", true

	case "lark_image":
		if source != "lark" && source != "larkcli" {
			return "lark_image is only allowed for a Lark Inbox item under the source_only profile.", false
		}
		var a struct {
			Message string `json:"message"`
			ChatID  string `json:"chat_id"`
			Account int    `json:"account"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return "invalid arguments for lark_image under the source_only profile.", false
		}
		if chatID := strings.TrimSpace(a.ChatID); chatID != "" && chatID != itemChatID {
			return "cross-conversation image reads are blocked under the source_only profile.", false
		}
		if hasLarkBlock && a.Account != 0 && a.Account != itemAccount {
			return "cross-account image reads are blocked under the source_only profile.", false
		}
		if !ownsMsg(a.Message) {
			return "lark_image must reference a message belonging to the bound Inbox item under the source_only profile.", false
		}
		return "", true
	}

	return name + " is not allowed under the source_only profile.", false
}

// excludedMessageToolTarget checks whether args targets an explicitly excluded chat ID even outside source_only.
func excludedMessageToolTarget(vis assistantwire.InboxVisibility, name string, args json.RawMessage) bool {
	if !vis.Active() {
		return false
	}
	switch name {
	case "larkcli_messages", "lark_messages", "lark_image":
	default:
		return false
	}
	if len(args) == 0 {
		return false
	}
	var a struct {
		ChatID string `json:"chat_id"`
	}
	if json.Unmarshal(args, &a) != nil {
		return false
	}
	return vis.ExcludesChat(a.ChatID, false)
}
