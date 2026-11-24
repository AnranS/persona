package node

import (
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// An assistant process learns its conversation's information categories (#13246): the track's default categories from
// assistant.prepare go into PERSONA_CATEGORIES next to PERSONA_SPACE and PERSONA_TRACK_KIND (empty string for general,
// comma-separated sorted names for sensitive); per-session categories from assistant.send override the directory's
// defaults, and categories_unknown (or an older manager or invalid token) leaves PERSONA_CATEGORIES unset so a command
// treats it as unknown (sensitive). An Agent started from the assistant's shell never takes it over.
func TestAssistantProcessKnowsItsCategories(t *testing.T) {
	t.Setenv("PERSONA_SKILLS_DIR", t.TempDir())
	t.Setenv("PERSONA_PLUGIN_SKILLS_DIR", t.TempDir())
	n, _ := testNode(t)
	n.dir, n.bin, n.logger = filepath.Join(n.home, ".persona"), "/opt/example/personad", log.New(io.Discard, "", 0)
	if err := os.MkdirAll(n.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	prepare := func(dir string, cats any) string {
		t.Helper()
		p := map[string]any{"dir": dir, "claude_md": "# Example"}
		if cats != nil {
			p["categories"] = cats
		}
		params, _ := json.Marshal(p)
		out, err := n.handle(t.Context(), "assistant.prepare", params)
		if err != nil {
			t.Fatal(err)
		}
		return out.(map[string]any)["cwd"].(string)
	}
	envOf := func(cwd, session string) []string {
		t.Helper()
		env, release := n.assistantLarkEnv(cwd, session)
		release()
		return env
	}
	hasCategoriesEnv := func(env []string) (string, bool) {
		for _, kv := range env {
			if v, ok := strings.CutPrefix(kv, categoriesEnv+"="); ok {
				return v, true
			}
		}
		return "", false
	}

	// Main directory prepared with empty list -> known general (PERSONA_CATEGORIES=).
	own := prepare(".persona/assistant", []string{})
	if val, ok := hasCategoriesEnv(envOf(own, "")); !ok || val != "" {
		t.Fatalf("general dir: ok=%v val=%q", ok, val)
	}
	// Health directory prepared with health -> PERSONA_CATEGORIES=health.
	health := prepare(".persona/health", []string{"health"})
	if val, ok := hasCategoriesEnv(envOf(health, "")); !ok || val != "health" {
		t.Fatalf("health dir: ok=%v val=%q", ok, val)
	}
	// Multiple unsorted/duplicate categories are normalized (sorted, deduplicated).
	multi := prepare(".persona/tracks/qa-mix", []string{"finance", "emotion", "finance", "legal_id"})
	if val, ok := hasCategoriesEnv(envOf(multi, "")); !ok || val != "emotion,finance,legal_id" {
		t.Fatalf("multi dir: ok=%v val=%q", ok, val)
	}

	// Older manager (no categories field) or invalid category token -> PERSONA_CATEGORIES absent (unknown/sensitive).
	for _, bad := range []any{nil, []string{"Bad Cat"}, []string{"health", "qa\nX=1"}, []string{""}} {
		cwd := prepare(".persona/assistant", bad)
		if val, ok := hasCategoriesEnv(envOf(cwd, "")); ok {
			t.Errorf("categories %#v: got %s=%q, want absent", bad, categoriesEnv, val)
		}
	}

	// Per-session categories override the prepared directory's defaults, and categories_unknown clears them.
	own = prepare(".persona/assistant", []string{})
	tagged := []string{"finance", "emotion"}
	n.setSessionCategories("qa-conv-1", &tagged, false)
	if val, ok := hasCategoriesEnv(envOf(own, "qa-conv-1")); !ok || val != "emotion,finance" {
		t.Fatalf("per-session tagged: ok=%v val=%q, want emotion,finance", ok, val)
	}
	// Unrelated session in the same directory still uses the directory's default.
	if val, ok := hasCategoriesEnv(envOf(own, "qa-conv-other")); !ok || val != "" {
		t.Fatalf("other session in general dir: ok=%v val=%q, want empty", ok, val)
	}
	// Older manager sending neither categories nor categories_unknown leaves existing session state alone.
	n.setSessionCategories("qa-conv-1", nil, false)
	if val, ok := hasCategoriesEnv(envOf(own, "qa-conv-1")); !ok || val != "emotion,finance" {
		t.Fatalf("older manager no-op: ok=%v val=%q, want emotion,finance", ok, val)
	}
	// categories_unknown (e.g. turn waiting to be tagged) removes PERSONA_CATEGORIES even though dir is general.
	n.setSessionCategories("qa-conv-1", nil, true)
	if val, ok := hasCategoriesEnv(envOf(own, "qa-conv-1")); ok {
		t.Fatalf("categories_unknown: got %s=%q, want absent", categoriesEnv, val)
	}
	// Invalid token on session also fails closed (absent).
	invalid := []string{"finance", "BAD"}
	n.setSessionCategories("qa-conv-1", &invalid, false)
	if val, ok := hasCategoriesEnv(envOf(own, "qa-conv-1")); ok {
		t.Fatalf("invalid session token: got %s=%q, want absent", categoriesEnv, val)
	}
	// Known general again restores PERSONA_CATEGORIES=.
	general := []string{}
	n.setSessionCategories("qa-conv-1", &general, false)
	if val, ok := hasCategoriesEnv(envOf(own, "qa-conv-1")); !ok || val != "" {
		t.Fatalf("restored general: ok=%v val=%q, want empty", ok, val)
	}

	if !slices.Contains(assistantOnlyEnvNames(), categoriesEnv) {
		t.Error("an Agent would take PERSONA_CATEGORIES over from the assistant's pane")
	}
}
