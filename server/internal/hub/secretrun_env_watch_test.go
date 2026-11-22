package hub

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

func secretRunWatchCard(id, delivery string) proto.Interaction {
	input, _ := json.Marshal(map[string]any{
		"machine":      "mac",
		"machine_name": "Mac mini",
		"purpose":      "Authenticate the one-off LAN CLI",
		"commands":     []string{"printf 'ready\\n'"},
		"delivery":     delivery,
	})
	summary := "Run with the password you type.\n$ printf 'ready\\n'"
	if delivery == "env" {
		summary = "Every listed command receives PERSONA_SECRET.\n$ printf 'ready\\n'"
	}
	return proto.Interaction{
		ID:        id,
		MachineID: "server",
		SessionID: "s1",
		Kind:      proto.IxPermission,
		ToolName:  proto.SecretRunTool,
		ToolInput: input,
		Summary:   summary,
		Fields:    []proto.Field{{Key: "password", Label: "Password", Secret: true}},
		CreatedAt: time.Now().UTC(),
		ExpiresAt: time.Now().Add(10 * time.Minute).UTC(),
	}
}

// An answer-only Watch credential can decline env delivery, never approve it. A typed value is forbidden by the hub;
// a quick approval without one follows the existing manager refusal, just as stdin does.
func TestSecretRunEnvWatchCanOnlyDecline(t *testing.T) {
	h := newTestHub(t)
	h.store = &tokenStore{memStore: *newMemStore(), rows: map[string]db.DeviceToken{}, rev: map[string]bool{}}
	mux := h.routes()
	_, phone := call(t, mux, http.MethodPost, "/api/v1/auth/tokens", h.cfg.Token, `{"name":"iPhone","kind":"phone"}`)
	_, watch := call(t, mux, http.MethodPost, "/api/v1/auth/tokens", phone["token"].(string), `{"name":"Watch","kind":"watch"}`)
	watchToken := watch["token"].(string)
	connectAssistant(t, h)

	const watchPassword = "watch-SECRET-must-not-be-relayed"
	for _, tc := range []struct {
		name, id, delivery, body string
		want                     int
	}{
		{name: "quick env approval", id: "ix_env_watch_quick", delivery: "env", body: `{"allow":true}`, want: http.StatusGone},
		{name: "typed env approval", id: "ix_env_watch_typed", delivery: "env", body: `{"allow":true,"values":{"password":"` + watchPassword + `"}}`, want: http.StatusForbidden},
		{name: "env decline", id: "ix_env_watch_decline", delivery: "env", body: `{"allow":false}`, want: http.StatusGone},
		{name: "legacy stdin approval", id: "ix_stdin_watch", delivery: "stdin", body: `{"allow":true}`, want: http.StatusGone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reportInteraction(t, h, secretRunWatchCard(tc.id, tc.delivery))
			code, body := call(t, mux, http.MethodPost, "/api/v1/interactions/"+tc.id+"/resolve", watchToken, tc.body)
			if code != tc.want {
				t.Errorf("Watch answer %s: %d %v, want %d", tc.body, code, body, tc.want)
			}
			if tc.want == http.StatusForbidden {
				if _, pending := h.pendingInteraction(tc.id); !pending {
					t.Error("a refused Watch approval consumed the env card")
				}
			}
			if strings.Contains(tc.body, watchPassword) {
				if response, _ := json.Marshal(body); strings.Contains(string(response), watchPassword) {
					t.Errorf("the Watch response contains the password: %s", response)
				}
			}
		})
	}
}
