package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/extwire"
)

// direction_sessions is a manager capability, not something a non-default manager or an extension report can promise.
func TestDirectionSessionsFollowTheDefaultManager(t *testing.T) {
	const feature = "direction_sessions"
	h, srv, _ := newBusHub(t)
	a := connectStateManager(t, h, srv, assistantwire.AssistantID)
	b := connectStateManager(t, h, srv, "m-0d0d0d")
	hello := func(m *stateManager, enabled bool) {
		t.Helper()
		caps := []string{assistantwire.CapabilityConfigOwn}
		if enabled {
			caps = append(caps, feature)
		}
		if err := m.call(t, busHello, map[string]any{"id": m.id, "capabilities": caps}, nil); err != nil {
			t.Fatal(err)
		}
	}
	listed := func() bool { return slices.Contains(h.features(), feature) }
	if listed() {
		t.Fatal("listed before any manager named direction_sessions")
	}
	hello(b, true)
	if listed() {
		t.Fatal("listed for a non-default manager")
	}
	hello(a, true)
	if !listed() {
		t.Fatal("default manager named direction_sessions, but the hub did not list it")
	}

	hello(a, false)
	a.notify(t, extwire.EventReport, extwire.Report{Features: []string{feature, "direction-report-control"}})
	eventually(t, 5*time.Second, "extension report", func() bool { return slices.Contains(h.features(), "direction-report-control") })
	if listed() {
		t.Fatal("an extension report added direction_sessions")
	}
	hello(a, true)
	if err := a.ws.CloseNow(); err != nil {
		t.Fatal(err)
	}
	eventually(t, 5*time.Second, "default manager offline", func() bool { return h.busConnOf(a.id) == nil })
	if listed() {
		t.Fatal("listed while the default manager is offline")
	}
}

func TestDirectionSessionRoutesAndFullCredentialScopes(t *testing.T) {
	patterns := []string{
		"PUT /api/v1/assistant/sessions/{mid}/{sid}/direction",
		"GET /api/v1/assistant/directions/{id}",
	}
	registered := routePatterns(t)
	for _, pattern := range patterns {
		if !slices.Contains(registered, pattern) {
			t.Errorf("assistantwire.Routes does not relay %s", pattern)
		}
		if scope := extwire.RouteScopes[pattern]; scope != "" {
			t.Errorf("%s must require full credentials, got extension scope %q", pattern, scope)
		}
	}
	data, err := os.ReadFile("testdata/route_scopes.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, pattern := range patterns {
		line := fmt.Sprintf("%-20s %s", "-", pattern)
		if !strings.Contains(string(data), line) {
			t.Errorf("route_scopes.txt lacks full-credentials-only row %q", line)
		}
	}
}

func TestDirectionSessionRoutesRelayToTheManager(t *testing.T) {
	h, srv, _ := newBusHub(t)
	got := make(chan assistantwire.HTTPRequest, 2)
	helloAssistant(t, h, srv, func(_ context.Context, method string, params json.RawMessage) (any, error) {
		if method == assistantwire.MethodTools {
			return map[string]any{"tools": []any{}}, nil
		}
		var req assistantwire.HTTPRequest
		if method != assistantwire.MethodHTTP || json.Unmarshal(params, &req) != nil {
			return nil, errors.New("unexpected " + method)
		}
		got <- req
		return assistantwire.HTTPResponse{Status: http.StatusCreated, Body: []byte(`{"relayed":true}`)}, nil
	})
	for _, call := range []struct{ method, path, body string }{
		{http.MethodPut, "/api/v1/assistant/sessions/mac/chat-a/direction", `{"direction":"conv_lead_a"}`},
		{http.MethodGet, "/api/v1/assistant/directions/conv_lead_a?title=Release&before=todo-1", ""},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		req, err := http.NewRequestWithContext(ctx, call.method, srv.URL+call.path, strings.NewReader(call.body))
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+h.cfg.Token)
		resp, err := http.DefaultClient.Do(req)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if resp.StatusCode != http.StatusCreated || string(body) != `{"relayed":true}` {
			t.Fatalf("%s %s was not relayed: %d %s", call.method, call.path, resp.StatusCode, body)
		}
		forwarded := <-got
		path, query, _ := strings.Cut(call.path, "?")
		if forwarded.Method != call.method || forwarded.Path != path || forwarded.Query != query || string(forwarded.Body) != call.body {
			t.Fatalf("manager received %+v, want %s %s body %q", forwarded, call.method, call.path, call.body)
		}
	}
}
