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
	s, _ := newQuietService(t)
	ctx, now := context.Background(), time.Now()
	entry := prompts.SuperviseEntry{ID: "todo_delta", Title: "Synthetic", Kind: sigReport,
		DoneWhen: []string{"criterion"}, Asked: "owner request", Last: "last", Evidence: []string{"evidence"}}
	first := superviseSignal{Todo: entry.ID, Kind: sigReport, Report: upReportQuestion, Key: "first", Recipient: "conv1"}
	if _, kept, _, err := s.reserveSupervision(ctx, []prompts.SuperviseEntry{entry}, []superviseSignal{first}, now); err != nil || len(kept) != 1 {
		t.Fatalf("first reservation: %v, %+v", err, kept)
	}
	second := first
	second.Key, second.Recipient = "second", "conv2"
	secondEntry := entry
	secondEntry.Last = "changed so the signal remains news"
	_, kept, _, err := s.reserveSupervision(ctx, []prompts.SuperviseEntry{secondEntry}, []superviseSignal{second}, now.Add(time.Minute))
	if err != nil || len(kept) != 1 {
		t.Fatalf("second recipient reservation: %v, %+v", err, kept)
	}
	g := kept[0]
	if g.RepeatCriteria != "" || g.RepeatRequest != "" || g.RepeatLast != "" || g.RepeatSaid != "" || len(g.RepeatEvidence) != 0 {
		t.Fatalf("one recipient's history suppressed another recipient's first facts: %+v", g)
	}
}

func TestIssue5665RepeatRollbackAndChangedRevalidationFailOpen(t *testing.T) {
	s, _ := newQuietService(t)
	ctx, now := context.Background(), time.Now()
	entry := prompts.SuperviseEntry{ID: "todo_delta", Title: "Synthetic", Kind: sigReport,
		DoneWhen: []string{"criterion"}, Last: "last", Evidence: []string{"old evidence"}}
	first := superviseSignal{Todo: entry.ID, Kind: sigReport, Report: upReportQuestion, Key: "first", Recipient: "conv1"}
	if _, _, _, err := s.reserveSupervision(ctx, []prompts.SuperviseEntry{entry}, []superviseSignal{first}, now); err != nil {
		t.Fatal(err)
	}
	second := first
	second.Key = "second"
	secondEntry := entry
	secondEntry.Last = "new last"
	_, kept, rollback, err := s.reserveSupervision(ctx, []prompts.SuperviseEntry{secondEntry}, []superviseSignal{second}, now.Add(time.Minute))
	if err != nil || len(kept) != 1 || len(kept[0].RepeatEvidence) != 1 {
		t.Fatalf("second reservation did not mark repeated evidence: %+v, %v", kept, err)
	}
	rollback()
	third := second
	third.Key = "third"
	thirdEntry := secondEntry
	thirdEntry.Evidence = []string{"evidence only seen in the rolled-back attempt"}
	_, kept, _, err = s.reserveSupervision(ctx, []prompts.SuperviseEntry{thirdEntry}, []superviseSignal{third}, now.Add(2*time.Minute))
	if err != nil || len(kept) != 1 {
		t.Fatalf("third reservation: %v, %+v", err, kept)
	}
	if len(kept[0].RepeatEvidence) != 0 {
		t.Fatal("rolled-back evidence became a delivered repeat")
	}

	// A repeat hash from an earlier reservation must not hide content changed while the signal waited.
	fourth := third
	fourth.Key = "fourth"
	fourthEntry := thirdEntry
	fourthEntry.Last = "another last"
	_, kept, _, err = s.reserveSupervision(ctx, []prompts.SuperviseEntry{fourthEntry}, []superviseSignal{fourth}, now.Add(3*time.Minute))
	if err != nil || len(kept) != 1 {
		t.Fatalf("fourth reservation: %v, %+v", err, kept)
	}
	fourthEntry.Evidence = append(fourthEntry.Evidence, "new evidence during revalidation")
	text := s.renderSupervisionDelivery(ctx, "conv1", []prompts.SuperviseEntry{fourthEntry}, kept)
	if !strings.Contains(text, "new evidence during revalidation") {
		t.Fatalf("a stale repeat hash hid new evidence:\n%s", text)
	}
}
