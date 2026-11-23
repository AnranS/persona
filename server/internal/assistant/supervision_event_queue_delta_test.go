package assistant

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

func issue5665Queue(t *testing.T, fx *issue5665Fixture, signals ...superviseSignal) {
	t.Helper()
	fx.f.setSessions("server", proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusWorking})
	ctx := context.Background()
	fx.s.updateSupervisePending(func(pending map[string]superviseSignal) {
		for _, g := range signals {
			pending[g.id()] = g
		}
	})
	fx.s.supervise.dedupMu.Lock()
	queued := fx.s.deliverDigest(ctx, fx.s.settings(ctx), "conv1", signals, issue5665Web, time.Now(), time.Time{})
	fx.s.supervise.dedupMu.Unlock()
	if !queued {
		t.Fatal("supervision message was not queued")
	}
	if got := len(assistantSendsTo(fx.f, "conv1")); got != 0 {
		t.Fatalf("queued fixture sent %d messages before the drain", got)
	}
}

func issue5665DrainText(t *testing.T, fx *issue5665Fixture) string {
	t.Helper()
	idle := proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle}
	fx.f.setSessions("server", idle)
	fx.s.drainRelays("server", []proto.Session{idle})
	waitFor(t, "supervision queue drain", func() bool {
		fx.s.relays.mu.Lock()
		defer fx.s.relays.mu.Unlock()
		return !fx.s.relays.draining["conv1"]
	})
	sends := assistantSendsTo(fx.f, "conv1")
	if len(sends) != 1 {
		t.Fatalf("drain produced %d sends, want one", len(sends))
	}
	text, _ := sends[0]["text"].(string)
	return text
}

func issue5665RecheckText(t *testing.T, fx *issue5665Fixture) string {
	t.Helper()
	fx.s.relays.mu.Lock()
	msg := fx.s.relays.st.Queue["conv1"][0]
	fx.s.relays.mu.Unlock()
	checked, stale, keep := fx.s.recheckQueuedDigest(context.Background(), "conv1", msg)
	if stale || keep {
		t.Fatalf("revalidation did not produce a deliverable message: stale=%v keep=%v", stale, keep)
	}
	if err := fx.s.tryDirectRelay(context.Background(), "server", "conv1", &checked); err != nil {
		t.Fatal(err)
	}
	sends := assistantSendsTo(fx.f, "conv1")
	if len(sends) != 1 {
		t.Fatalf("revalidated delivery produced %d sends, want one", len(sends))
	}
	text, _ := sends[0]["text"].(string)
	return text
}

func issue5665RequireFirstBriefAndEvidence(t *testing.T, text string) {
	t.Helper()
	for _, want := range []string{issue5665Criterion, issue5665Request, issue5665Evidence} {
		if !issue5665Contains(text, want) {
			t.Errorf("first actual delivery lost %q:\n%s", want, text)
		}
	}
}

func issue5665RequireFirstDeliveryFacts(t *testing.T, text string) {
	t.Helper()
	issue5665RequireFirstBriefAndEvidence(t, text)
	if !issue5665Contains(text, issue5665AgentLast) {
		t.Errorf("first actual delivery lost %q:\n%s", issue5665AgentLast, text)
	}
}

func TestIssue5665UnsentReplacementKeepsFirstDeliveryComplete(t *testing.T) {
	fx := newIssue5665Fixture(t)
	first := superviseSignal{Todo: fx.todo.ID, Kind: sigInterrupted, Key: "queued-first", Detail: issue5665FirstReason,
		Machine: "box", Session: issue5665AgentID, At: time.Now()}
	issue5665Queue(t, fx, first)

	fx.f.setSessions("box", proto.Session{SessionID: issue5665AgentID, MachineID: "box", Agent: "claude", Status: proto.StatusInterrupted,
		State: proto.StateUnread, LastText: issue5665NextReason, UpdatedAt: time.Now()})
	second := superviseSignal{Todo: fx.todo.ID, Kind: sigInterrupted, Key: "queued-replacement", Detail: issue5665NextReason,
		Machine: "box", Session: issue5665AgentID, At: time.Now().Add(time.Minute)}
	issue5665Queue(t, fx, second)

	text := issue5665DrainText(t, fx)
	issue5665RequireFirstBriefAndEvidence(t, text)
	if !strings.Contains(text, issue5665NextReason) {
		t.Errorf("replacement's changed Agent text is missing:\n%s", text)
	}
}

func TestIssue5665MootBatchMemberDoesNotLeaveDelta(t *testing.T) {
	fx := newIssue5665Fixture(t)
	if _, _, err := fx.s.updateSupervision(context.Background(), fx.todo.ID, func(v *Supervision) bool {
		v.State = reviewNeedsUser
		return true
	}); err != nil {
		t.Fatal(err)
	}
	first := superviseSignal{Todo: fx.todo.ID, Kind: sigUndecidable, Key: "queued-undecidable", Detail: "synthetic wait",
		Machine: "box", Session: issue5665AgentID, At: time.Now()}
	second := superviseSignal{Todo: fx.todo.ID, Kind: sigInterrupted, Key: "queued-interrupted", Detail: issue5665FirstReason,
		Machine: "box", Session: issue5665AgentID, At: time.Now()}
	issue5665Queue(t, fx, first, second)
	if _, _, err := fx.s.updateSupervision(context.Background(), fx.todo.ID, func(v *Supervision) bool {
		v.State = reviewPending
		return true
	}); err != nil {
		t.Fatal(err)
	}

	issue5665RequireFirstDeliveryFacts(t, issue5665RecheckText(t, fx))
}

func TestIssue5665UnsentQueueDoesNotAdvancePayloadHistory(t *testing.T) {
	fx := newIssue5665Fixture(t)
	g := superviseSignal{Todo: fx.todo.ID, Kind: sigInterrupted, Key: "queued-only", Detail: issue5665FirstReason,
		Machine: "box", Session: issue5665AgentID, At: time.Now(), Recipient: "conv1"}
	issue5665Queue(t, fx, g)
	if _, ok := fx.s.supervise.payloads[superviseEventPayloadKey("conv1", g.Todo)]; ok {
		t.Fatal("an unsent reservation advanced the source recipient's payload history")
	}
}
