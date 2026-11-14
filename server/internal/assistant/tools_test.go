package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// shortAgentStartTimeout makes the start wait and adoption pause expire in milliseconds, not the production bounds.
func shortAgentStartTimeout(t *testing.T) {
	t.Helper()
	oldTimeout, oldRetry := agentStartTimeout, agentStartAdoptRetry
	agentStartTimeout, agentStartAdoptRetry = 30*time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { agentStartTimeout, agentStartAdoptRetry = oldTimeout, oldRetry })
}

// The node built the session and launched the agent but its answer never came back: not a failure, and the model
// must not start a second copy. The start itself is attempted exactly once.
func TestAgentStartSlowAnswerIsUnknownNotFailure(t *testing.T) {
	s, f := newTaskService(t)
	shortAgentStartTimeout(t)
	f.slowStart = true
	res, err := s.runTool(context.Background(), "server", "conv9", "agent_start",
		json.RawMessage(`{"machine":"box","cwd":"/code/app","prompt":"看看 CI"}`))
	if err != nil {
		t.Fatalf("unknown start result must not be an error: %v", err)
	}
	if !strings.Contains(res, "may already be starting") || !strings.Contains(res, "Do NOT start it again") ||
		!strings.Contains(res, "Agents tab") {
		t.Fatalf("neutral text missing guidance:\n%s", res)
	}
	if len(f.starts) != 1 {
		t.Fatalf("the start was attempted %d times, want 1 (no automatic retry)", len(f.starts))
	}
}

// A late node whose session list shows the new session has the start confirmed and linked like an answered one.
func TestAgentStartAdoptsTheSessionThatStartedLate(t *testing.T) {
	s, f := newQuietService(t)
	shortAgentStartTimeout(t)
	f.slowStart = true
	f.onStart = func(f *fakeHub, a map[string]any) { // the node adds the session (with its launcher) while handling the late start
		f.sessions["box"] = []proto.Session{{SessionID: "s9", MachineID: "box", Cwd: "/code/app", Agent: "claude",
			Launcher: a["launcher"].(string), StartedByPersona: true, Status: proto.StatusWorking,
			State: proto.StateWorking, UpdatedAt: time.Now().UTC()}}
	}
	res, err := s.runTool(context.Background(), "server", "conv1", "agent_start",
		json.RawMessage(`{"machine":"box","cwd":"/code/app","prompt":"看看 CI"}`))
	if err != nil {
		t.Fatalf("adopting the started session must not error: %v", err)
	}
	if !strings.Contains(res, "s9") || !strings.Contains(res, "confirmed from the machine's own list") {
		t.Fatalf("result does not confirm the adopted session:\n%s", res)
	}
	if !strings.Contains(res, "a task of this conversation") {
		t.Fatalf("an adopted quiet task must be described like a normal quiet start:\n%s", res)
	}
	todo := onlyTodo(s)
	if len(todo.Agents) != 1 || todo.Agents[0].SessionID != "s9" {
		t.Fatalf("adopted session not linked as a quiet task: %+v", todo)
	}
}

// The user can close the pre-created card while session.start is in flight. The node has still launched the Agent:
// return started-with-warning, keep it linked to the closed card for cleanup, and never invite a duplicate start.
func TestAgentStartReportsStartedWhenTaskClosesDuringLaunch(t *testing.T) {
	s, f := newQuietService(t)
	f.onStart = func(f *fakeHub, a map[string]any) {
		id, _ := a["creation_id"].(string)
		now := time.Now().UTC()
		s.todos.mu.Lock()
		if todo := s.todos.todos[id]; todo != nil {
			todo.DoneAt = &now
		}
		s.todos.mu.Unlock()
	}
	res, err := s.runTool(context.Background(), "server", "conv1", "agent_start",
		json.RawMessage(`{"machine":"box","cwd":"/code/app","prompt":"看看 CI"}`))
	if err != nil {
		t.Fatalf("a successfully started Agent must not read as a start failure: %v", err)
	}
	if !strings.Contains(res, "Started on ") || !strings.Contains(res, "Warning") || !strings.Contains(res, "Do NOT start it again") {
		t.Fatalf("started-with-warning result = %q", res)
	}
	if len(f.starts) != 1 {
		t.Fatalf("the start was attempted %d times, want 1", len(f.starts))
	}
	todo := onlyTodo(s)
	if todo.DoneAt == nil || len(todo.Agents) != 1 {
		t.Fatalf("the started Agent is not linked to the closed card for cleanup: %+v", todo)
	}
}

func requireStartUnknownState(t *testing.T, s *Service, todoID string, want bool) {
	t.Helper()
	key := "start-unknown@" + todoID
	s.supervise.mu.Lock()
	pending := s.supervise.pending[todoID+"|"+sigUnjudged]
	s.supervise.mu.Unlock()
	durable := storedSupervisePending(t, s)[todoID+"|"+sigUnjudged]
	s.tasks.turns.mu.Lock()
	var ledger *turnSignal
	if sig := s.tasks.turns.doc.Turns[todoID].Signal; sig != nil {
		copy := *sig
		ledger = &copy
	}
	s.tasks.turns.mu.Unlock()
	if want {
		if pending.Key != key || durable.Key != key {
			t.Fatalf("start-unknown is not pending everywhere: memory=%+v durable=%+v", pending, durable)
		}
		return
	}
	if pending.Key == key || durable.Key == key {
		t.Fatalf("start-unknown survived confirmation: memory=%+v durable=%+v", pending, durable)
	}
	if ledger != nil && ledger.Kind == sigUnjudged && ledger.Key == key && ledger.Told == nil {
		t.Fatalf("start-unknown can still replay from the turn ledger: %+v", ledger)
	}
}

// stalledAssistantSendHub blocks a relay send without fakeHub's mutex held, so the test can link the confirmed session
// while that send is genuinely in flight.
type stalledAssistantSendHub struct {
	*fakeHub
	started  chan struct{}
	release  chan struct{}
	attempts chan string
	calls    atomic.Int32
}

func (h *stalledAssistantSendHub) DeviceCall(ctx context.Context, p DeviceCall) (json.RawMessage, error) {
	if p.Method != DeviceAssistantSend {
		return h.fakeHub.DeviceCall(ctx, p)
	}
	var params map[string]any
	_ = json.Unmarshal(p.Params, &params)
	text, _ := params["text"].(string)
	h.attempts <- text
	if h.calls.Add(1) == 1 {
		close(h.started)
		<-h.release
	}
	return nil, errors.New("send failed")
}

// An unknown start result can be confirmed from either the node's creation identity or its bounded session adoption.
// If the card closed during the same launch, both paths retain the Agent on that done card and report the confirmed session.
func TestAgentStartUnknownReportsStartedWhenTaskClosesDuringLaunch(t *testing.T) {
	for _, tc := range []struct {
		name       string
		creationID bool
		want       string
	}{
		{"creation id", true, "confirmed by its creation id"},
		{"adopted session", false, "confirmed from the machine's own list"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f := newQuietServiceOn(t, &memStore{})
			shortAgentStartTimeout(t)
			f.slowStart = true
			f.onStart = func(f *fakeHub, a map[string]any) {
				id, _ := a["creation_id"].(string)
				now := time.Now().UTC()
				s.todos.mu.Lock()
				if todo := s.todos.todos[id]; todo != nil {
					todo.DoneAt = &now
				}
				s.todos.mu.Unlock()
				s.signal(superviseSignal{Todo: id, Kind: sigUnjudged, Key: "start-unknown@" + id, Detail: "earlier unknown start"})
				sess := proto.Session{SessionID: "s-late", MachineID: "box", Cwd: "/code/app", Agent: "claude",
					Launcher: a["launcher"].(string), StartedByPersona: true, Status: proto.StatusWorking,
					State: proto.StateWorking, UpdatedAt: now}
				if tc.creationID {
					sess.Hierarchy = &proto.SessionHierarchy{CreationID: id}
				}
				f.sessions["box"] = []proto.Session{sess}
			}
			res, err := s.runTool(context.Background(), "server", "conv1", "agent_start",
				json.RawMessage(`{"machine":"box","cwd":"/code/app","prompt":"看看 CI"}`))
			if err != nil {
				t.Fatalf("a confirmed Agent must not read as a start failure: %v", err)
			}
			if !strings.Contains(res, "Started on ") || !strings.Contains(res, tc.want) ||
				!strings.Contains(res, "task closed while the Agent started") || !strings.Contains(res, "Do NOT start it again") {
				t.Fatalf("confirmed started-with-warning result = %q", res)
			}
			if len(f.starts) != 1 {
				t.Fatalf("the start was attempted %d times, want 1", len(f.starts))
			}
			todo := onlyTodo(s)
			if todo.DoneAt == nil || len(todo.Agents) != 1 || todo.Agents[0].SessionID != "s-late" {
				t.Fatalf("the confirmed Agent is not linked to the closed card for cleanup: %+v", todo)
			}
			requireStartUnknownState(t, s, todo.ID, false)
		})
	}
}

// An unanswered start still has its task from before the request. When the session appears later, its persisted
// creation id links it back to that same card without guessing from cwd or adopting a manual session (#5732).
func TestAgentStartUnknownKeepsPendingTaskAndLinksByCreationID(t *testing.T) {
	s, f := newQuietServiceOn(t, &memStore{})
	shortAgentStartTimeout(t)
	f.slowStart = true
	res, err := s.runTool(context.Background(), "server", "conv1", "agent_start",
		json.RawMessage(`{"machine":"box","cwd":"/code/app","prompt":"看看 CI"}`))
	if err != nil || !strings.Contains(res, "may already be starting") {
		t.Fatalf("unknown start: res=%q err=%v", res, err)
	}
	todo := onlyTodo(s)
	if todo.Origin != "conv1" || todo.Supervision == nil || todo.Supervision.Supervisor != "conv1" || len(todo.Agents) != 0 {
		t.Fatalf("pending task = %+v", todo)
	}
	if len(f.starts) != 1 || f.starts[0]["creation_id"] != todo.ID || f.starts[0]["incarnation"] == "" {
		t.Fatalf("start lacks task identity: %+v", f.starts)
	}
	requireStartUnknownState(t, s, todo.ID, true)

	// The session list arrives later with the node's persisted creation identity. It joins the pending card directly.
	s.autoLinkTodos("box", []proto.Session{{SessionID: "s-late", MachineID: "box", Cwd: "/code/app", Agent: "claude",
		Hierarchy: &proto.SessionHierarchy{CreationID: todo.ID}, StartedByPersona: true, Status: proto.StatusWorking, UpdatedAt: time.Now()}})
	got := todoNow(s, todo.ID)
	if len(got.Agents) != 1 || got.Agents[0].SessionID != "s-late" || got.Agents[0].Source != "assistant_start" {
		t.Fatalf("late session not linked: %+v", got)
	}
	requireStartUnknownState(t, s, todo.ID, false)
	// A later, genuinely unjudged stop has another key and is not part of this retraction.
	s.signal(superviseSignal{Todo: todo.ID, Kind: sigUnjudged, Key: "later-real-stop", Detail: "a later judge failure"})
	if _, err := s.linkStartedAgentTask(context.Background(), got, Device{ID: "box"}, "/code/app", "s-other"); err != nil {
		t.Fatal(err)
	}
	requireStartUnknownState(t, s, todo.ID, false)
	s.supervise.mu.Lock()
	pending := s.supervise.pending[todo.ID+"|"+sigUnjudged]
	s.supervise.mu.Unlock()
	if pending.Key != "later-real-stop" || storedSupervisePending(t, s)[todo.ID+"|"+sigUnjudged].Key != "later-real-stop" {
		t.Fatalf("confirmation deleted the later unjudged stop: memory=%+v durable=%+v", pending, storedSupervisePending(t, s)[todo.ID+"|"+sigUnjudged])
	}
}

// A creation-id confirmation can arrive after the start wait has ended and the user has closed the prepared card.
// That exact session still belongs on the done card for cleanup; an unrelated manual session gains no link (#5732).
func TestAgentStartUnknownLinksDelayedCreationIDAfterTaskClosed(t *testing.T) {
	s, f := newQuietServiceOn(t, &memStore{})
	shortAgentStartTimeout(t)
	f.slowStart = true
	res, err := s.runTool(context.Background(), "server", "conv1", "agent_start",
		json.RawMessage(`{"machine":"box","cwd":"/code/app","prompt":"看看 CI"}`))
	if err != nil || !strings.Contains(res, "may already be starting") {
		t.Fatalf("unknown start: res=%q err=%v", res, err)
	}
	todo := onlyTodo(s)
	requireStartUnknownState(t, s, todo.ID, true)
	now := time.Now().UTC()
	s.todos.mu.Lock()
	s.todos.todos[todo.ID].DoneAt = &now
	s.todos.mu.Unlock()

	linked := s.autoLinkTodos("box", []proto.Session{
		{SessionID: "s-late", MachineID: "box", Cwd: "/code/app", Agent: "claude", StartedByPersona: true, Status: proto.StatusWorking,
			Hierarchy: &proto.SessionHierarchy{CreationID: todo.ID}, UpdatedAt: now},
		{SessionID: "manual", MachineID: "box", Cwd: "/code/app", Agent: "claude", Status: proto.StatusWorking, UpdatedAt: now},
	})
	if !linked {
		t.Fatal("a retained delayed creation-id link was not reported to the session-list caller")
	}
	got := todoNow(s, todo.ID)
	if got.DoneAt == nil || len(got.Agents) != 1 || got.Agents[0].SessionID != "s-late" || got.Agents[0].Source != "assistant_start" {
		t.Fatalf("the delayed creation-id session is not retained on the closed card: %+v", got)
	}
	if active := s.todos.activeBySession[[2]string{"box", "s-late"}]; active != "" {
		t.Fatalf("the done card became active again as %q", active)
	}
	for _, a := range got.Agents {
		if a.SessionID == "manual" {
			t.Fatalf("an unrelated manual session was auto-linked: %+v", got.Agents)
		}
	}
	requireStartUnknownState(t, s, todo.ID, false)
}

// Queue admission is another durable form of the provisional result. A later exact confirmation must keep it from
// replaying after a restart, not only clear the outbox and turn ledger (#5732).
func TestAgentStartConfirmationRetractsQueuedStartUnknown(t *testing.T) {
	s, _ := newQuietServiceOn(t, &memStore{})
	presenceTimes(t, 0, 0)
	ownerTurnTimes(t, 0, 0)
	todo, err := s.createTodo(context.Background(), "Confirm the start", "", "conv1", "")
	if err != nil {
		t.Fatal(err)
	}
	s.superviseTodo(context.Background(), todo.ID, "conv1", nil, 0)
	todo = todoNow(s, todo.ID)
	g := superviseSignal{Todo: todo.ID, Kind: sigUnjudged, Key: "start-unknown@" + todo.ID, Detail: "stale unknown start"}
	other := superviseSignal{Todo: todo.ID, Kind: sigStalled, Key: "later-real-stop", Detail: "a later genuine stop"}
	signals := []superviseSignal{g, other}
	if !s.queueDigest("conv1", relayMsg{Tool: superviseTool, Text: "stale unknown start", At: time.Now()}, signals, time.Now(), time.Time{}) {
		t.Fatal("the unknown start was not admitted to the durable relay queue")
	}
	if _, err := s.linkStartedAgentTask(context.Background(), todo, Device{ID: "box"}, "/code/app", "s-confirmed"); err != nil {
		t.Fatal(err)
	}
	s.supervise.mu.Lock()
	queued := len(s.supervise.queued["conv1"])
	s.supervise.mu.Unlock()
	if queued != 0 {
		t.Fatal("the confirmed start-unknown remained in the in-memory queue")
	}
	simulateSupervisionRestart(t, s)
	if got := queuedRelays(s, "conv1"); len(got) != 0 {
		t.Fatalf("the confirmed start-unknown remained queued: %+v", got)
	}
	if got := storedSupervisePending(t, s)[other.id()]; got.Key != other.Key {
		t.Fatalf("the queued retraction deleted the later genuine stop: %+v", got)
	}
	idle := proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle}
	s.drainRelays("server", []proto.Session{idle})
}

// A send already in flight owns its original text. Once that try fails, however, the confirmed start must not be sent
// again from the retained queue head, including after a restart (#5732).
func TestAgentStartConfirmationWithdrawsDrainingStartUnknown(t *testing.T) {
	s, f := newQuietServiceOn(t, &memStore{})
	s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
	presenceTimes(t, 0, 0)
	ownerTurnTimes(t, 0, 0)
	todo, err := s.createTodo(context.Background(), "Confirm during a send", "", "conv1", "")
	if err != nil {
		t.Fatal(err)
	}
	s.superviseTodo(context.Background(), todo.ID, "conv1", nil, 0)
	todo = todoNow(s, todo.ID)
	g := superviseSignal{Todo: todo.ID, Kind: sigUnjudged, Key: "start-unknown@" + todo.ID, Detail: "stale unknown start"}
	other := superviseSignal{Todo: todo.ID, Kind: sigStalled, Key: "later-real-stop", Detail: "a later genuine stop"}
	signals := []superviseSignal{g, other}
	if !s.queueDigest("conv1", relayMsg{Tool: superviseTool, Text: "stale unknown start", At: time.Now()}, signals, time.Now(), time.Time{}) {
		t.Fatal("the unknown start was not admitted to the durable relay queue")
	}
	started, release := make(chan struct{}), make(chan struct{})
	hub := &stalledAssistantSendHub{fakeHub: f, started: started, release: release, attempts: make(chan string, 4)}
	setHub(s, hub)
	idle := proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle}
	s.drainRelays("server", []proto.Session{idle})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("the queue head did not enter its first send")
	}
	if text := <-hub.attempts; !strings.Contains(text, "stale unknown start") {
		t.Fatalf("the first send fixture = %q", text)
	}
	if _, err := s.linkStartedAgentTask(context.Background(), todo, Device{ID: "box"}, "/code/app", "s-confirmed"); err != nil {
		t.Fatal(err)
	}
	close(release)
	waitFor(t, "the failed first send to settle", func() bool { return !drainingNow(s, "conv1") })
	simulateSupervisionRestart(t, s)
	s.drainRelays("server", []proto.Session{idle})
	select {
	case text := <-hub.attempts:
		if strings.Contains(text, "stale unknown start") {
			t.Fatalf("the failed queue head replayed the confirmed start-unknown: %q", text)
		}
	case <-time.After(time.Second):
		t.Fatal("the other queued signal was not regathered after withdrawing the old head")
	}
}

// Definite failures stay errors: a request the node refused before running anything, and an unreachable machine
// (nothing was started) — never a neutral "may be starting" result.
func TestAgentStartDefiniteFailuresStayErrors(t *testing.T) {
	cases := []struct {
		name string
		set  func(*fakeHub)
		want func(error) bool
	}{
		{"refused", func(f *fakeHub) { f.failStart = &rpc.Error{Code: "open_elsewhere", Message: "open elsewhere"} },
			func(err error) bool { var re *rpc.Error; return errors.As(err, &re) && re.Code == "open_elsewhere" }},
		{"offline", func(f *fakeHub) { f.offline = map[string]bool{"box": true} },
			func(err error) bool { return errors.Is(err, apierr.ErrOffline) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, f := newTaskService(t)
			tc.set(f)
			res, err := s.runTool(context.Background(), "server", "conv9", "agent_start",
				json.RawMessage(`{"machine":"box","cwd":"/code/app","prompt":"看看 CI"}`))
			if err == nil || !tc.want(err) {
				t.Fatalf("%s: got res=%q err=%v", tc.name, res, err)
			}
		})
	}
}

// Which failures leave the start's outcome unknown, and which are definite.
func TestStartResultUnknown(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"deadline", context.DeadlineExceeded, true},
		{"canceled", context.Canceled, true},
		{"closed", rpc.ErrClosed, true},
		{"dropped", rpc.ErrDropped, true},
		{"relay words", &rpc.Error{Message: rpc.ErrClosed.Error()}, true},
		{"wrapped deadline", fmt.Errorf("session.start: %w", context.DeadlineExceeded), true},
		{"coded refusal", &rpc.Error{Code: "open_elsewhere", Message: "open elsewhere"}, false},
		{"offline", apierr.ErrOffline, false},
		{"no device", ErrNoDevice, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := startResultUnknown(tc.err); got != tc.want {
				t.Fatalf("startResultUnknown(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// The adoption filter: new, hub-started, non-assistant, same launcher and directory, fresh sign of life.
func TestAdoptStartedSessionFilters(t *testing.T) {
	s, f := newTaskService(t)
	now := time.Now().UTC()
	mk := func(id string, fn func(*proto.Session)) proto.Session {
		sess := proto.Session{SessionID: id, MachineID: "box", Agent: "claude", Launcher: "claude-code",
			Cwd: "/code/app", StartedByPersona: true, UpdatedAt: now}
		fn(&sess)
		return sess
	}
	known := map[string]bool{"old": true}
	dev := Device{ID: "box"}
	// Adopted: a new cwd session, and a new one whose node-verified WorkspaceCwd is the directory.
	for _, sess := range []proto.Session{
		mk("new", func(*proto.Session) {}),
		mk("new", func(s *proto.Session) { s.Cwd = ""; s.WorkspaceCwd = "/code/app" }),
	} {
		f.setSessions("box", mk("old", func(*proto.Session) {}), sess)
		if got, ok := s.adoptStartedSession(context.Background(), dev, "claude-code", "/code/app", "", known); !ok || got.SessionID != "new" {
			t.Fatalf("got %+v ok=%v, want new adopted", got, ok)
		}
	}

	// Rejected: pre-existing, not hub-started, the assistant, other launcher/cwd, stale or never-seen sign of life.
	for _, tc := range []struct {
		name string
		fn   func(*proto.Session)
	}{
		{"not hub-started", func(s *proto.Session) { s.StartedByPersona = false }},
		{"the assistant itself", func(s *proto.Session) { s.Assistant = true }},
		{"other launcher", func(s *proto.Session) { s.Launcher = "codex" }},
		{"other directory", func(s *proto.Session) { s.Cwd = "/elsewhere" }},
		{"stale sign of life", func(s *proto.Session) { s.UpdatedAt = now.Add(-agentStartAdoptWindow - time.Minute) }},
		{"never seen", func(s *proto.Session) { s.UpdatedAt = time.Time{} }},
	} {
		f.setSessions("box", mk("old", func(*proto.Session) {}), mk("new", tc.fn))
		if _, ok := s.adoptStartedSession(context.Background(), dev, "claude-code", "/code/app", "", known); ok {
			t.Fatalf("%s session was adopted", tc.name)
		}
	}

	// A pre-start list that failed means no guessing afterwards.
	f.setSessions("box", mk("new", func(*proto.Session) {}))
	if _, ok := s.adoptStartedSession(context.Background(), dev, "claude-code", "/code/app", "", nil); ok {
		t.Fatal("adoption happened without a pre-start snapshot")
	}
}

// ask_user's answers come back keyed by question text: two questions worded alike (after trimming) are refused before
// any card goes on the phone, instead of one answer silently overwriting the other (#4517). Distinct wording still asks.
func TestAskUserRefusesDuplicateQuestionText(t *testing.T) {
	s := newTestService(t, newFakeHub())
	q := func(text string) map[string]any {
		return map[string]any{"question": text, "options": []map[string]any{{"label": "好"}, {"label": "不好"}}}
	}
	call := func(texts ...string) (proto.ToolResult, bool) {
		var qs []map[string]any
		for _, x := range texts {
			qs = append(qs, q(x))
		}
		raw, _ := json.Marshal(map[string]any{"questions": qs})
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		carded := make(chan bool, 1)
		go func() {
			for ctx.Err() == nil {
				if qs := s.pendingQuestions(); len(qs) == 1 {
					yes := map[string]string{}
					for _, x := range qs[0].Questions {
						yes[x.Question] = "好"
					}
					raw, _ := json.Marshal(proto.Resolution{Answers: yes})
					_, _ = s.Action(context.Background(), Action{Op: "answer", InteractionID: qs[0].ID, Answer: raw})
					carded <- true
					return
				}
				time.Sleep(5 * time.Millisecond)
			}
			carded <- false
		}()
		r := s.Tool(ctx, ToolCall{Name: "ask_user", Arguments: raw, MachineID: "mac", SessionID: "conv-assistant"})
		cancel()
		return r, <-carded
	}
	text := func(r proto.ToolResult) string {
		var b strings.Builder
		for _, c := range r.Content {
			b.WriteString(c.Text)
		}
		return b.String()
	}

	r, carded := call("发吗？", "  发吗？ ")
	if !r.IsError || text(r) != i18n.Err("hub-hub.duplicate_question").Error() {
		t.Fatalf("duplicate questions: error=%v %q", r.IsError, text(r))
	}
	if carded {
		t.Fatal("a card went on the phone for duplicate questions")
	}

	r, carded = call("发吗？", "现在发吗？")
	if r.IsError || !carded || !strings.Contains(text(r), "发吗？ → 好") || !strings.Contains(text(r), "现在发吗？ → 好") {
		t.Fatalf("distinct questions: carded=%v error=%v %q", carded, r.IsError, text(r))
	}
}

// agent_send's readonly refusal is original MCP model text, not a localized user-facing notification or card.
func TestAgentSendReadonlyPreservesModelResult(t *testing.T) {
	s, f := newTaskService(t)
	const reason = "Original source diagnostic / 原始原因 / 元の理由"
	for _, caps := range []*proto.SessionCapabilities{nil, {Read: true}} {
		f.setSessions("box", proto.Session{SessionID: "readonly-agent", MachineID: "box", Cwd: "/code/app",
			SourceKind: "external-history", Capabilities: caps, ReadOnlyReason: reason})
		for _, locale := range []string{"en", "zh-Hans", "ja", "en"} {
			f.mu.Lock()
			f.hello.Locale = locale
			f.mu.Unlock()
			res := s.Tool(i18n.With(context.Background(), i18n.Lang(locale)), ToolCall{
				MachineID: "server", SessionID: "conv9", Name: "agent_send",
				Arguments: json.RawMessage(`{"machine":"box","session_id":"readonly-agent","text":"Continue"}`),
			})
			if !res.IsError || len(res.Content) != 1 || res.Content[0].Text != "这个 Agent 仅支持查看："+reason {
				t.Fatalf("locale=%q capabilities=%+v: %+v", locale, caps, res)
			}
		}
	}
	if sent := sentToAgent(f); len(sent) != 0 {
		t.Fatalf("readonly agent received messages: %v", sent)
	}
	if len(s.pendingQuestions()) != 0 || len(f.pushed()) != 0 {
		t.Fatal("model-only refusal created a user-facing card or push")
	}
}
