package assistant

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/prompts"
)

func TestIssue5665TodoGetIncludesAuthoritativeRequestAndScopeHistory(t *testing.T) {
	fx := newIssue5665Fixture(t)
	brief, ok := fx.s.briefOfTodo(fx.todo.ID)
	if !ok {
		t.Fatal("missing synthetic brief")
	}
	_, _, err := fx.s.changeBrief(context.Background(), brief.ID, func(tb *taskBrief) bool {
		tb.st.Log = append(tb.st.Log, BriefEvent{
			Kind: briefEventScope, At: time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC),
			Change: "Keep only the synthetic crash fix", Why: "The owner narrowed the example",
			Dropped: []string{"synthetic dropped criterion"},
		})
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := runJSON(t, fx.s, "conv1", "todo_get", map[string]any{"id": fx.todo.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{issue5665Request, "scope change at", "Keep only the synthetic crash fix", "The owner narrowed the example", "synthetic dropped criterion"} {
		if !strings.Contains(got, want) {
			t.Errorf("todo_get lacks authoritative brief fact %q:\n%s", want, got)
		}
	}
}

func TestIssue5665PayloadHistoryIsRecipientScoped(t *testing.T) {
	fx := newIssue5665Fixture(t)
	entry := prompts.SuperviseEntry{ID: fx.todo.ID, Title: "Synthetic", Kind: sigReport,
		DoneWhen: []string{"criterion"}, Asked: "owner request", Last: "last", Evidence: []string{"evidence"}}
	fx.s.ackSupervisionPayload(context.Background(), "conv1", relayMsg{Tool: superviseTool,
		Payloads: encodeSupervisionPayloads([]prompts.SuperviseEntry{entry})})
	text, _ := fx.s.renderSupervisionDelivery(context.Background(), "conv2", []prompts.SuperviseEntry{entry})
	for _, want := range []string{"criterion", "owner request", "evidence", "last"} {
		if !strings.Contains(text, want) {
			t.Errorf("one recipient's acknowledged history suppressed another's first facts %q:\n%s", want, text)
		}
	}
}

func TestIssue5665UnsentRenderAndChangedRevalidationFailOpen(t *testing.T) {
	fx := newIssue5665Fixture(t)
	ctx := context.Background()
	entry := prompts.SuperviseEntry{ID: fx.todo.ID, Title: "Synthetic", Kind: sigReport,
		DoneWhen: []string{"criterion"}, Last: "last", Evidence: []string{"old evidence"}}
	for attempt := 0; attempt < 2; attempt++ {
		text, _ := fx.s.renderSupervisionDelivery(ctx, "conv1", []prompts.SuperviseEntry{entry})
		for _, want := range []string{"criterion", "old evidence", "last"} {
			if !strings.Contains(text, want) {
				t.Fatalf("unsent render %d lost %q:\n%s", attempt+1, want, text)
			}
		}
	}
	if _, ok := fx.s.supervise.payloads[superviseEventPayloadKey("conv1", entry.ID)]; ok {
		t.Fatal("rendering without a successful send advanced payload history")
	}

	fx.s.ackSupervisionPayload(ctx, "conv1", relayMsg{Tool: superviseTool,
		Payloads: encodeSupervisionPayloads([]prompts.SuperviseEntry{entry})})
	changed := entry
	changed.Evidence = []string{"old evidence", "new evidence during revalidation"}
	text, _ := fx.s.renderSupervisionDelivery(ctx, "conv1", []prompts.SuperviseEntry{changed})
	if strings.Contains(text, "old evidence") || !strings.Contains(text, "new evidence during revalidation") {
		t.Fatalf("acknowledged and changed evidence were not distinguished:\n%s", text)
	}
	changed.Evidence = []string{"new evidence during revalidation"}
	text, _ = fx.s.renderSupervisionDelivery(ctx, "conv1", []prompts.SuperviseEntry{changed})
	if !strings.Contains(text, "new evidence during revalidation") {
		t.Fatalf("a stale repeat hash hid new evidence:\n%s", text)
	}
}
