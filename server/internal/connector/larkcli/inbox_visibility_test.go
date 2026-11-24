package larkcli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	adapter "github.com/nwverse/persona/server/internal/larkcli"
)

func TestInboxVisibilityDefaultsUnchanged(t *testing.T) {
	dm := adapter.Message{
		MessageID:  "om_default_dm",
		ChatID:     "oc_example_dm",
		ChatType:   "p2p",
		CreateTime: "2030-01-01T00:00:00Z",
		Sender:     adapter.Sender{ID: "ou_example_peer", Name: "Peer"},
		Content:    json.RawMessage(`"Private direct message"`),
	}
	group := adapter.Message{
		MessageID:  "om_default_group",
		ChatID:     "oc_example_group",
		ChatType:   "group",
		ChatName:   "Engineering Group",
		CreateTime: "2030-01-01T00:01:00Z",
		Sender:     adapter.Sender{ID: "ou_example_peer", Name: "Peer"},
		Mentions:   []adapter.Mention{{ID: "ou_example_user"}},
		Content:    json.RawMessage(`"Can you review this group update?"`),
	}
	c := followupConnector(t, []adapter.Message{dm, group}, []adapter.Message{dm, group})
	if moved, err := c.Poll(context.Background()); err != nil || !moved {
		t.Fatalf("default Poll: moved=%v err=%v", moved, err)
	}
	got := inboxThings(t, c)
	if _, ok := got["larkcli:chat:oc_example_dm"]; !ok {
		t.Fatalf("default behavior dropped direct chat: %+v", got)
	}
	if _, ok := got["larkcli:chat:oc_example_group"]; !ok {
		t.Fatalf("default behavior dropped group chat: %+v", got)
	}
}

func TestInboxExcludeDirectChatsFiltersSearchSweepAndContext(t *testing.T) {
	dm := adapter.Message{
		MessageID:  "om_priv_dm",
		ChatID:     "oc_example_dm",
		ChatType:   "p2p",
		CreateTime: "2030-01-01T00:00:00Z",
		Sender:     adapter.Sender{ID: "ou_example_peer", Name: "Private Peer"},
		Content:    json.RawMessage(`"Secret 1:1 DM"`),
	}
	botDM := adapter.Message{
		MessageID:  "om_priv_bot",
		ChatID:     "oc_example_bot",
		ChatType:   "p2p",
		CreateTime: "2030-01-01T00:00:30Z",
		Sender:     adapter.Sender{ID: "cli_bot_peer", SenderType: "app", Name: "Bot"},
		Content:    json.RawMessage(`"Bot DM alert"`),
	}
	group := adapter.Message{
		MessageID:  "om_allowed_group",
		ChatID:     "oc_example_group",
		ChatType:   "group",
		ChatName:   "Allowed Group",
		CreateTime: "2030-01-01T00:01:00Z",
		Sender:     adapter.Sender{ID: "ou_example_peer", Name: "Teammate"},
		Mentions:   []adapter.Mention{{ID: "ou_example_user"}},
		Content:    json.RawMessage(`"Allowed group question"`),
	}

	logFile := filepath.Join(t.TempDir(), "cli.log")
	t.Setenv("LARKCLI_FAKE_LOG", logFile)
	chatsFile := filepath.Join(t.TempDir(), "chats.json")
	if err := os.WriteFile(chatsFile, []byte(`{"ok":true,"data":{"chats":[{"chat_id":"oc_example_dm","chat_type":"p2p"},{"chat_id":"oc_example_bot","chat_type":"p2p"}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LARKCLI_FAKE_CHATS", chatsFile)

	c := followupConnector(t, []adapter.Message{dm, botDM, group}, []adapter.Message{group})
	if err := c.SetOptions(json.RawMessage(`{"exclude_direct_chats":true}`)); err != nil {
		t.Fatalf("SetOptions: %v", err)
	}
	if moved, err := c.Poll(context.Background()); err != nil || !moved {
		t.Fatalf("Poll: moved=%v err=%v", moved, err)
	}
	got := inboxThings(t, c)
	if len(got) != 1 {
		t.Fatalf("expected only allowed group, got %d items: %+v", len(got), got)
	}
	if _, ok := got["larkcli:chat:oc_example_group"]; !ok {
		t.Fatalf("allowed group missing: %+v", got)
	}
	if _, ok := got["larkcli:chat:oc_example_dm"]; ok {
		t.Fatalf("human DM leaked into inboxThings: %+v", got)
	}
	if _, ok := got["larkcli:chat:oc_example_bot"]; ok {
		t.Fatalf("bot DM leaked into inboxThings: %+v", got)
	}

	rawLog, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read CLI log: %v", err)
	}
	logText := string(rawLog)
	if strings.Contains(logText, "+chat-list --types p2p") {
		t.Fatalf("direct-chat sweep invoked ListDirectChats despite exclude_direct_chats=true:\n%s", logText)
	}
	if strings.Contains(logText, "oc_example_dm") || strings.Contains(logText, "oc_example_bot") {
		t.Fatalf("direct chats triggered downstream CLI context fetches:\n%s", logText)
	}
}

func TestInboxExcludedChatIDsFilterSearchAndDirectSweepIndependently(t *testing.T) {
	// Search returns an excluded group (even though it's in inbox_all_chats and @me) and an allowed group.
	excludedGroup := adapter.Message{
		MessageID:  "om_excl_group",
		ChatID:     "oc_example_excluded_group",
		ChatType:   "group",
		ChatName:   "Excluded Group",
		CreateTime: "2030-01-01T00:00:00Z",
		Sender:     adapter.Sender{ID: "ou_example_peer", Name: "Peer"},
		Mentions:   []adapter.Mention{{ID: "ou_example_user"}},
		Content:    json.RawMessage(`"Sensitive group thread"`),
	}
	allowedGroup := adapter.Message{
		MessageID:  "om_ok_group",
		ChatID:     "oc_example_allowed_group",
		ChatType:   "group",
		ChatName:   "Allowed Group",
		CreateTime: "2030-01-01T00:01:00Z",
		Sender:     adapter.Sender{ID: "ou_example_peer", Name: "Peer"},
		Mentions:   []adapter.Mention{{ID: "ou_example_user"}},
		Content:    json.RawMessage(`"Normal group thread"`),
	}
	// Direct sweep returns one excluded DM and one allowed DM (not present in search results).
	allowedDM := adapter.Message{
		MessageID:  "om_ok_dm",
		ChatID:     "oc_example_allowed_dm",
		ChatType:   "p2p",
		CreateTime: "2030-01-01T00:02:00Z",
		Sender:     adapter.Sender{ID: "ou_example_peer", Name: "Allowed Peer"},
		Content:    json.RawMessage(`"Allowed DM from sweep"`),
	}

	logFile := filepath.Join(t.TempDir(), "cli.log")
	t.Setenv("LARKCLI_FAKE_LOG", logFile)
	chatsFile := filepath.Join(t.TempDir(), "chats.json")
	if err := os.WriteFile(chatsFile, []byte(`{"ok":true,"data":{"chats":[{"chat_id":"oc_example_excluded_dm","chat_type":"p2p"},{"chat_id":"oc_example_allowed_dm","chat_type":"p2p"}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LARKCLI_FAKE_CHATS", chatsFile)

	c := followupConnector(t, []adapter.Message{excludedGroup, allowedGroup}, []adapter.Message{allowedGroup, allowedDM})
	c.now = func() time.Time { return time.Date(2030, 1, 1, 0, 5, 0, 0, time.UTC) }
	if err := c.SetOptions(json.RawMessage(`{
		"inbox_all_chats": ["oc_example_excluded_group"],
		"excluded_chat_ids": ["oc_example_excluded_group", "oc_example_excluded_dm"]
	}`)); err != nil {
		t.Fatalf("SetOptions: %v", err)
	}

	if moved, err := c.Poll(context.Background()); err != nil || !moved {
		t.Fatalf("Poll: moved=%v err=%v", moved, err)
	}
	got := inboxThings(t, c)
	if _, ok := got["larkcli:chat:oc_example_excluded_group"]; ok {
		t.Fatalf("excluded group bypassed filter via inbox_all_chats/@me: %+v", got)
	}
	if _, ok := got["larkcli:chat:oc_example_excluded_dm"]; ok {
		t.Fatalf("excluded DM bypassed direct-chat sweep filter: %+v", got)
	}
	if _, ok := got["larkcli:chat:oc_example_allowed_group"]; !ok {
		t.Fatalf("allowed group missing: %+v", got)
	}
	if _, ok := got["larkcli:chat:oc_example_allowed_dm"]; !ok {
		t.Fatalf("allowed DM from sweep missing: %+v", got)
	}

	rawLog, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read CLI log: %v", err)
	}
	logText := string(rawLog)
	if strings.Contains(logText, "oc_example_excluded_group") {
		t.Fatalf("excluded group triggered downstream CLI call:\n%s", logText)
	}
	if strings.Contains(logText, "oc_example_excluded_dm") {
		t.Fatalf("excluded DM triggered ListMessagesSince in direct-chat sweep:\n%s", logText)
	}
}

func TestLarkCLIToolsEnforceVisibilityExclusions(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "cli.log")
	t.Setenv("LARKCLI_FAKE_LOG", logFile)

	// Use the default fake scenario where chats.json / search.json / chat_get_p2p.json exist.
	c := testConnector(t, "success")
	c.SetVisibilityDefaults(true, []string{"oc_example_excluded"})
	c.noteDirectChat("oc_example_dm")

	// Known direct chat read must fail closed with ZERO CLI calls.
	if _, err := c.Call(context.Background(), "larkcli_messages", json.RawMessage(`{"chat_id":"oc_example_dm"}`)); err == nil {
		t.Fatal("larkcli_messages allowed reading a known direct chat")
	}
	// Explicitly excluded group read must fail closed with ZERO CLI calls.
	if _, err := c.Call(context.Background(), "larkcli_messages", json.RawMessage(`{"chat_id":"oc_example_excluded"}`)); err == nil {
		t.Fatal("larkcli_messages allowed reading an excluded chat")
	}
	// Unverified thread-only read must fail closed with ZERO CLI calls.
	if _, err := c.Call(context.Background(), "larkcli_messages", json.RawMessage(`{"thread":"omt_unknown"}`)); err == nil {
		t.Fatal("larkcli_messages allowed reading an unverified thread")
	}
	if data, err := os.ReadFile(logFile); err == nil && len(data) > 0 {
		t.Fatalf("rejected larkcli_messages calls invoked downstream CLI:\n%s", string(data))
	}

	// Allowed group read succeeds.
	out, err := c.Call(context.Background(), "larkcli_messages", json.RawMessage(`{"chat_id":"oc_example_group"}`))
	if err != nil {
		t.Fatalf("allowed group larkcli_messages failed: %v", err)
	}
	if !strings.Contains(out, "messages") {
		t.Fatalf("unexpected allowed group output: %s", out)
	}

	// larkcli_chats uses --types group when exclude_direct_chats is enabled.
	if _, err := c.Call(context.Background(), "larkcli_chats", json.RawMessage(`{"filter":"recent"}`)); err != nil {
		t.Fatalf("larkcli_chats failed: %v", err)
	}
	rawLog, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read CLI log: %v", err)
	}
	if !strings.Contains(string(rawLog), "+chat-list --types group") {
		t.Fatalf("larkcli_chats did not restrict to group chats:\n%s", string(rawLog))
	}
}

func TestVisibilityOptionsValidationAndPruning(t *testing.T) {
	c := testConnector(t, "success")
	for _, bad := range []string{
		`{"exclude_direct_chats":"yes"}`,
		`{"excluded_chat_ids":"oc_1"}`,
		`{"excluded_chat_ids":[""]}`,
		`{"excluded_chat_ids":["oc_1","oc_1"]}`,
	} {
		if err := c.SetOptions(json.RawMessage(bad)); err == nil {
			t.Fatalf("SetOptions(%s) unexpectedly succeeded", bad)
		}
	}

	// Round-trip valid options and verify pruning of existing in-memory excluded things.
	dm := adapter.Message{MessageID: "om_1", ChatID: "oc_dm", ChatType: "p2p", CreateTime: "2030-01-01T00:00:00Z", Sender: adapter.Sender{ID: "ou_peer"}, Content: json.RawMessage(`"hi"`)}
	grp := adapter.Message{MessageID: "om_2", ChatID: "oc_grp", ChatType: "group", CreateTime: "2030-01-01T00:00:00Z", Sender: adapter.Sender{ID: "ou_peer"}, Mentions: []adapter.Mention{{ID: "ou_example_user"}}, Content: json.RawMessage(`"hi"`)}
	fc := followupConnector(t, []adapter.Message{dm, grp}, []adapter.Message{dm, grp})
	if _, err := fc.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := inboxThings(t, fc); len(got) != 2 {
		t.Fatalf("expected 2 items before exclusion, got %d", len(got))
	}
	if err := fc.SetOptions(json.RawMessage(`{"exclude_direct_chats":true,"excluded_chat_ids":["oc_extra"]}`)); err != nil {
		t.Fatalf("SetOptions: %v", err)
	}
	if got := inboxThings(t, fc); len(got) != 1 || got["larkcli:chat:oc_grp"].Thread == "" {
		t.Fatalf("SetOptions did not prune excluded direct chat from memory: %+v", got)
	}
	var roundTripped InboxOptions
	if err := json.Unmarshal(fc.Options(), &roundTripped); err != nil {
		t.Fatal(err)
	}
	if !roundTripped.ExcludeDirectChats || len(roundTripped.ExcludedChatIDs) != 1 || roundTripped.ExcludedChatIDs[0] != "oc_extra" {
		t.Fatalf("unexpected Options round-trip: %+v", roundTripped)
	}
}
