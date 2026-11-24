package extmgr

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
	larkcliconnector "github.com/nwverse/persona/server/internal/connector/larkcli"
	"github.com/nwverse/persona/server/internal/lark"
)

func TestLoadConfigValidatesInboxVisibilityAndReadProfile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hub.json"), []byte(`{"token":"hub-token"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// Valid opt-in configuration loads and normalizes cleanly.
	goodFile := filepath.Join(t.TempDir(), "manager.json")
	good := `{
		"inbox": {
			"read_profile": "source_only",
			"lark": {
				"exclude_direct_chats": true,
				"excluded_chat_ids": [" oc_b ", "oc_a"]
			}
		}
	}`
	if err := os.WriteFile(goodFile, []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(dir, goodFile)
	if err != nil {
		t.Fatalf("LoadConfig valid: %v", err)
	}
	if cfg.Inbox.ReadProfile != assistantwire.InboxReadProfileSourceOnly {
		t.Fatalf("ReadProfile = %q, want %q", cfg.Inbox.ReadProfile, assistantwire.InboxReadProfileSourceOnly)
	}
	if !cfg.Inbox.Lark.ExcludeDirectChats || !slices.Equal(cfg.Inbox.Lark.ExcludedChatIDs, []string{"oc_a", "oc_b"}) {
		t.Fatalf("Lark visibility = %+v, want exclude_direct_chats=true [oc_a oc_b]", cfg.Inbox.Lark)
	}

	// Invalid read_profile or excluded_chat_ids must be rejected.
	for _, bad := range []string{
		`{"inbox":{"read_profile":"unrestricted"}}`,
		`{"inbox":{"agent":{"read_profile":"bogus"}}}`,
		`{"inbox":{"lark":{"excluded_chat_ids":[""]}}}`,
		`{"inbox":{"lark":{"excluded_chat_ids":["oc_dup","oc_dup"]}}}`,
	} {
		badFile := filepath.Join(t.TempDir(), "manager.json")
		if err := os.WriteFile(badFile, []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(dir, badFile); err == nil {
			t.Fatalf("LoadConfig(%s) unexpectedly succeeded", bad)
		}
	}
}

func TestInboxConfigViewUnionsManagerAndConnectorVisibility(t *testing.T) {
	reg := connector.NewRegistry(nil)
	h := &Manager{
		cfg: &Config{
			Lark: lark.Config{CLI: &lark.CLIConfig{Bin: "/nonexistent/lark-cli"}},
			Inbox: InboxConfig{
				ReadProfile: assistantwire.InboxReadProfileSourceOnly,
				Lark: assistantwire.InboxVisibility{
					ExcludedChatIDs: []string{"oc_from_config"},
				},
			},
		},
		connectors: reg,
	}
	lc := h.newLarkCLIConnector("example")
	if err := lc.SetOptions(json.RawMessage(`{"exclude_direct_chats":true,"excluded_chat_ids":["oc_from_connector"]}`)); err != nil {
		t.Fatalf("SetOptions: %v", err)
	}
	if err := reg.Add(lc); err != nil {
		t.Fatalf("reg.Add: %v", err)
	}

	view := h.inboxConfigView(context.Background())
	if view.EffectiveReadProfile() != assistantwire.InboxReadProfileSourceOnly || !view.SourceOnly() {
		t.Fatalf("view.ReadProfile = %q, want source_only", view.ReadProfile)
	}
	if !view.Lark.ExcludeDirectChats {
		t.Fatalf("view.Lark.ExcludeDirectChats = false, want true")
	}
	wantIDs := []string{"oc_from_config", "oc_from_connector"}
	if !slices.Equal(view.Lark.ExcludedChatIDs, wantIDs) {
		t.Fatalf("view.Lark.ExcludedChatIDs = %v, want %v", view.Lark.ExcludedChatIDs, wantIDs)
	}
	// Connector instance also received manager.json defaults via newLarkCLIConnector.
	if _, err := lc.Call(context.Background(), "larkcli_messages", json.RawMessage(`{"chat_id":"oc_from_config"}`)); err == nil {
		t.Fatal("newLarkCLIConnector did not apply manager.json excluded_chat_ids default")
	}
	_ = larkcliconnector.ID
}
