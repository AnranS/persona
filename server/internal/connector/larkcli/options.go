package larkcli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/nwverse/persona/server/internal/connector"
	adapter "github.com/nwverse/persona/server/internal/larkcli"
)

// InboxOptions is the complete lark-cli connector settings object. Chat and
// document Inbox settings share one object so options.get/options.set can
// round-trip them without one poller overwriting the other.
type InboxOptions struct {
	InboxAllChats      []string `json:"inbox_all_chats"`
	ExcludeDirectChats bool     `json:"exclude_direct_chats,omitempty"`
	ExcludedChatIDs    []string `json:"excluded_chat_ids,omitempty"`
	Documents          []string `json:"documents"`
	WikiSpaces         []string `json:"wiki_spaces"`
	// OwnedAllComments: every comment on a watched document the user owns (DocumentOptions).
	OwnedAllComments bool `json:"owned_all_comments"`
	// TriageRules: what counts as Needs you / FYI / Collapsed for lark-cli's items, in the user's words.
	TriageRules string `json:"triage_rules"`
}

func (c *Connector) Options() json.RawMessage {
	c.ensureDocuments()
	c.inboxMu.Lock()
	ids := make([]string, 0, len(c.inboxAllChats))
	for id := range c.inboxAllChats {
		ids = append(ids, id)
	}
	excludeDirect := c.excludeDirectChats
	excluded := make([]string, 0, len(c.excludedChatIDs))
	for id := range c.excludedChatIDs {
		excluded = append(excluded, id)
	}
	rules := c.triageRules
	c.inboxMu.Unlock()
	sort.Strings(ids)
	sort.Strings(excluded)

	c.docs.mu.Lock()
	documents := c.docs.options.normalized()
	c.docs.mu.Unlock()
	if documents.Documents == nil {
		documents.Documents = []string{}
	}
	if documents.WikiSpaces == nil {
		documents.WikiSpaces = []string{}
	}
	b, _ := json.Marshal(InboxOptions{
		InboxAllChats:      ids,
		ExcludeDirectChats: excludeDirect,
		ExcludedChatIDs:    excluded,
		Documents:          documents.Documents,
		WikiSpaces:         documents.WikiSpaces,
		OwnedAllComments:   documents.OwnedAllComments,
		TriageRules:        rules,
	})
	return b
}

func (c *Connector) SetOptions(raw json.RawMessage) error {
	var wire struct {
		InboxAllChats      json.RawMessage `json:"inbox_all_chats"`
		ExcludeDirectChats json.RawMessage `json:"exclude_direct_chats"`
		ExcludedChatIDs    json.RawMessage `json:"excluded_chat_ids"`
		TriageRules        *string         `json:"triage_rules"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return fmt.Errorf("options: %v", err)
	}

	chats, err := parseChatIDSet(wire.InboxAllChats, "options.inbox_all_chats")
	if err != nil {
		return err
	}
	var excludeDirect bool
	if len(wire.ExcludeDirectChats) > 0 && string(wire.ExcludeDirectChats) != "null" {
		if err := json.Unmarshal(wire.ExcludeDirectChats, &excludeDirect); err != nil {
			return fmt.Errorf("options.exclude_direct_chats: must be a boolean")
		}
	}
	excluded, err := parseChatIDSet(wire.ExcludedChatIDs, "options.excluded_chat_ids")
	if err != nil {
		return err
	}

	documents, err := parseDocumentOptions(raw)
	if err != nil {
		return err
	}
	c.ensureDocuments()
	rules := ""
	if wire.TriageRules != nil {
		rules = strings.TrimSpace(*wire.TriageRules)
	}
	if len([]rune(rules)) > maxTriageRules {
		return fmt.Errorf("options.triage_rules is longer than %d characters", maxTriageRules)
	}
	c.inboxMu.Lock()
	c.inboxAllChats = chats
	c.excludeDirectChats = excludeDirect
	c.excludedChatIDs = excluded
	c.triageRules = rules
	c.pruneExcludedThingsLocked()
	c.inboxMu.Unlock()
	c.docs.setOptions(documents)
	return nil
}

func parseChatIDSet(raw json.RawMessage, field string) (map[string]struct{}, error) {
	var idsValue []string
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &idsValue); err != nil {
			return nil, fmt.Errorf("%s: must be an array of chat ids", field)
		}
	}
	seen := make(map[string]struct{}, len(idsValue))
	chats := make(map[string]struct{}, len(idsValue))
	for _, id := range idsValue {
		id = strings.TrimSpace(id)
		if id == "" {
			return nil, fmt.Errorf("%s contains an empty chat id", field)
		}
		if len(id) > 256 {
			return nil, fmt.Errorf("%s chat id is too long", field)
		}
		if _, ok := seen[id]; ok {
			return nil, fmt.Errorf("%s contains duplicate chat id %q", field, id)
		}
		seen[id] = struct{}{}
		chats[id] = struct{}{}
	}
	return chats, nil
}

// SetVisibilityDefaults configures manager.json visibility exclusions that union
// with the connector's own saved options.
func (c *Connector) SetVisibilityDefaults(excludeDirect bool, excludedChatIDs []string) {
	excluded := make(map[string]struct{}, len(excludedChatIDs))
	for _, id := range excludedChatIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			excluded[id] = struct{}{}
		}
	}
	c.inboxMu.Lock()
	c.defaultExcludeDirect = excludeDirect
	c.defaultExcludedChatIDs = excluded
	c.pruneExcludedThingsLocked()
	c.inboxMu.Unlock()
}

func (c *Connector) visibilityLocked() (bool, map[string]struct{}) {
	excludeDirect := c.excludeDirectChats || c.defaultExcludeDirect
	n := len(c.excludedChatIDs) + len(c.defaultExcludedChatIDs)
	if n == 0 {
		return excludeDirect, nil
	}
	merged := make(map[string]struct{}, n)
	for id := range c.defaultExcludedChatIDs {
		merged[id] = struct{}{}
	}
	for id := range c.excludedChatIDs {
		merged[id] = struct{}{}
	}
	return excludeDirect, merged
}

func (c *Connector) visibility() (bool, map[string]struct{}) {
	c.inboxMu.Lock()
	defer c.inboxMu.Unlock()
	return c.visibilityLocked()
}

func (c *Connector) noteDirectChat(chatID string) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return
	}
	c.inboxMu.Lock()
	if c.knownDirectChats == nil {
		c.knownDirectChats = map[string]struct{}{}
	}
	c.knownDirectChats[chatID] = struct{}{}
	c.inboxMu.Unlock()
}

func (c *Connector) isKnownDirectChat(chatID string) bool {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return false
	}
	c.inboxMu.Lock()
	_, ok := c.knownDirectChats[chatID]
	c.inboxMu.Unlock()
	return ok
}

func (c *Connector) chatIDExcluded(chatID string) bool {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return false
	}
	c.inboxMu.Lock()
	defer c.inboxMu.Unlock()
	_, excluded := c.visibilityLocked()
	_, ok := excluded[chatID]
	return ok
}

func (c *Connector) chatExcluded(chat adapter.Chat) bool {
	isDirect := chat.ChatType == "p2p" || chat.ChatMode == "p2p"
	if isDirect {
		c.noteDirectChat(chat.ChatID)
	}
	c.inboxMu.Lock()
	defer c.inboxMu.Unlock()
	excludeDirect, excluded := c.visibilityLocked()
	if excludeDirect && (isDirect || c.isKnownDirectChatLocked(chat.ChatID)) {
		return true
	}
	_, ok := excluded[strings.TrimSpace(chat.ChatID)]
	return ok
}

func (c *Connector) messageExcluded(message adapter.Message) bool {
	isDirect := message.ChatType == "p2p"
	if isDirect {
		c.noteDirectChat(message.ChatID)
	}
	c.inboxMu.Lock()
	defer c.inboxMu.Unlock()
	excludeDirect, excluded := c.visibilityLocked()
	if excludeDirect && (isDirect || c.isKnownDirectChatLocked(message.ChatID)) {
		return true
	}
	_, ok := excluded[strings.TrimSpace(message.ChatID)]
	return ok
}

func (c *Connector) groupExcluded(group *inboxGroup) bool {
	if group == nil {
		return false
	}
	isDirect := group.latest.ChatType == "p2p"
	for _, m := range group.messages {
		if m.ChatType == "p2p" {
			isDirect = true
			break
		}
	}
	if isDirect {
		c.noteDirectChat(group.chatID)
	}
	c.inboxMu.Lock()
	defer c.inboxMu.Unlock()
	excludeDirect, excluded := c.visibilityLocked()
	if excludeDirect && (isDirect || c.isKnownDirectChatLocked(group.chatID)) {
		return true
	}
	_, ok := excluded[strings.TrimSpace(group.chatID)]
	return ok
}

func (c *Connector) isKnownDirectChatLocked(chatID string) bool {
	if chatID == "" || c.knownDirectChats == nil {
		return false
	}
	_, ok := c.knownDirectChats[strings.TrimSpace(chatID)]
	return ok
}

func (c *Connector) thingExcludedLocked(thing connector.InboxThing) bool {
	excludeDirect, excluded := c.visibilityLocked()
	if !excludeDirect && len(excluded) == 0 {
		return false
	}
	chatID, ok := chatIDFromThread(thing.Thread)
	if !ok {
		for _, tag := range thing.Tags {
			if tag.Key == connector.TagChat && strings.TrimSpace(tag.Value) != "" {
				chatID = strings.TrimSpace(tag.Value)
				break
			}
		}
	}
	isDirect := thing.Kind == "dm" || thing.Kind == "bot"
	if excludeDirect && (isDirect || c.isKnownDirectChatLocked(chatID)) {
		return true
	}
	if chatID != "" {
		if _, ok := excluded[chatID]; ok {
			return true
		}
	}
	return false
}

func (c *Connector) pruneExcludedThingsLocked() {
	if len(c.inboxThings) == 0 {
		return
	}
	for thread, thing := range c.inboxThings {
		if c.thingExcludedLocked(thing) {
			delete(c.inboxThings, thread)
		}
	}
}

func (c *Connector) chatForThread(threadID string) (chatID string, direct bool, found bool) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return "", false, false
	}
	suffix := ":thread:" + threadID
	c.inboxMu.Lock()
	defer c.inboxMu.Unlock()
	for thread, thing := range c.inboxThings {
		if !strings.HasSuffix(thread, suffix) {
			continue
		}
		if id, ok := chatIDFromThread(thread); ok {
			return id, thing.Kind == "dm" || thing.Kind == "bot" || c.isKnownDirectChatLocked(id), true
		}
	}
	return "", false, false
}

// maxTriageRules is the Inbox's limit for a source's rules.
const maxTriageRules = 4000

// rules are the user's triage rules for lark-cli's items.
func (c *Connector) rules() string {
	c.inboxMu.Lock()
	defer c.inboxMu.Unlock()
	return c.triageRules
}

func (c *Connector) ensureDocuments() {
	if c.docs == nil {
		c.docs = newDocumentInbox()
	}
}
