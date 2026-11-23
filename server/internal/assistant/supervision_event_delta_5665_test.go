package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// These are synthetic UTF-8 payloads, not real requests or reports. i18n-ignore: prompt fixtures assert exact bytes.
const (
	issue5665Web         = "https://hub.example.test"
	issue5665AgentID     = "s1abcdef"
	issue5665Request     = "原始请求全文：只修复横屏登录崩溃；不得扩大审批、计划或通知范围。"
	issue5665Criterion   = "PR 合并到 master"
	issue5665Evidence    = "证据版本一：测试报告与合并记录已经核验"
	issue5665AgentLast   = "Agent 最后回复：已保留审批边界，等待下一步。\n第二行：没有修改计划或通知范围。"
	issue5665FirstReason = "第一次运行原因：模型服务返回 529"
	issue5665NextReason  = "新的运行原因：Agent 在第二次检查点停下"
	issue5665Report      = "未改变的汇报全文：审批仍然等待用户决定。\n第二行：不要修改计划。\n第三行：不要扩大通知范围。"
	issue5665OldEvidence = "证据甲：旧版测试报告全部通过"
	issue5665NewEvidence = "证据乙：新增的深色截图已经核验"
)

type issue5665Fixture struct {
	s    *Service
	f    *fakeHub
	todo Todo
	st   Store
}

func newIssue5665Fixture(t *testing.T) *issue5665Fixture {
	t.Helper()
	st := planUpdateStore{&planMemStore{memStore: &memStore{}, plans: map[string]db.TaskPlanRow{}}}
	s, f, todo := newSupervisedServiceOn(t, st)
	s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
	ctx := context.Background()
	brief, ok := s.briefOfTodo(todo.ID)
	if !ok {
		t.Fatal("synthetic task has no authoritative brief")
	}
	_, _, err := s.changeBrief(ctx, brief.ID, func(tb *taskBrief) bool {
		tb.rec.StartedBy = briefByOwner
		tb.rec.Request = &BriefRequest{Text: issue5665Request, At: time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC), Conversation: "conv1"}
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.linkTodoAgent(ctx, todo.ID, "box", issue5665AgentID, todo.Title, "/code/app", "claude"); err != nil {
		t.Fatal(err)
	}
	todo = todoNow(s, todo.ID)
	if err := s.recordCriterionReports(ctx, todo, "box", issue5665AgentID, []criterionReport{{
		Number: 1, State: "met", Evidence: issue5665Evidence,
	}}, time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	fx := &issue5665Fixture{s: s, f: f, todo: todoNow(s, todo.ID), st: st}
	fx.agent(f)
	return fx
}

// agent gives the actual delivery boundary one stable Agent answer and the matching readable transcript.
func (fx *issue5665Fixture) agent(f *fakeHub) {
	f.setSessions("box", proto.Session{SessionID: issue5665AgentID, MachineID: "box", Cwd: "/code/app", Agent: "claude",
		Status: proto.StatusDone, State: proto.StateUnread, LastText: issue5665AgentLast, UpdatedAt: time.Now()})
	f.mu.Lock()
	f.items = []proto.Item{
		{Role: "user", Kind: proto.KindText, Text: "Continue the synthetic task.", TS: time.Now().Add(-time.Minute)},
		{Role: "assistant", Kind: proto.KindText, Text: issue5665AgentLast, TS: time.Now()},
	}
	f.mu.Unlock()
}

// issue5665Deliver drives the real digest path and captures the resulting DeviceAssistantSend params. It also
// establishes the durable outbox entry required on a stored service, just as signal does before sendDigest.
func issue5665Deliver(t *testing.T, s *Service, f *fakeHub, to string, g superviseSignal, wantDelivery bool) map[string]any {
	t.Helper()
	ctx := context.Background()
	if err := s.ensureTodosLoaded(ctx); err != nil {
		t.Fatal(err)
	}
	s.updateSupervisePending(func(pending map[string]superviseSignal) { pending[g.id()] = g })
	before := len(assistantSendsTo(f, to))
	s.supervise.dedupMu.Lock()
	delivered := s.deliverDigest(ctx, s.settings(ctx), to, []superviseSignal{g}, issue5665Web, time.Now(), time.Time{})
	s.supervise.dedupMu.Unlock()
	sends := assistantSendsTo(f, to)
	if delivered != wantDelivery || len(sends) != before+map[bool]int{true: 1, false: 0}[wantDelivery] {
		t.Fatalf("delivery of %s/%s = %v, sends %d -> %d; want delivery %v", g.Todo, g.Kind, delivered, before, len(sends), wantDelivery)
	}
	if !wantDelivery {
		return nil
	}
	send := sends[len(sends)-1]
	text, _ := send["text"].(string)
	if text == "" || send["resume_only"] != true || send[holdPushesParam] != true {
		t.Fatalf("not a resumed, notification-holding DeviceAssistantSend: %+v", send)
	}
	if got, want := send["supervision_instructions"], prompts.RenderSupervisionInstructions(); got != want {
		t.Fatalf("runtime event changed stable policy transport: got %d bytes, want %d", len(got.(string)), len(want))
	}
	return send
}

// issue5665AssertDedup replays an already acknowledged whole signal without recreating its durable outbox entry:
// recreating that entry would intentionally select the undelivered-retry path instead of semantic deduplication.
func issue5665AssertDedup(t *testing.T, s *Service, f *fakeHub, g superviseSignal) {
	t.Helper()
	ctx := context.Background()
	before := len(assistantSendsTo(f, "conv1"))
	s.supervise.dedupMu.Lock()
	s.deliverDigest(ctx, s.settings(ctx), "conv1", []superviseSignal{g}, issue5665Web, time.Now(), time.Time{})
	s.supervise.dedupMu.Unlock()
	if got := len(assistantSendsTo(f, "conv1")); got != before {
		t.Fatalf("identical whole signal was delivered again: sends %d -> %d", before, got)
	}
}

func issue5665Text(send map[string]any) string {
	text, _ := send["text"].(string)
	return text
}

func issue5665Contains(text, fragment string) bool {
	return strings.Contains(strings.Join(strings.Fields(text), " "), strings.Join(strings.Fields(fragment), " "))
}

func issue5665Pending(t *testing.T, s *Service, id string) superviseSignal {
	t.Helper()
	s.supervise.mu.Lock()
	defer s.supervise.mu.Unlock()
	g, ok := s.supervise.pending[id]
	if !ok {
		t.Fatalf("no pending supervision signal %s", id)
	}
	return g
}

type issue5665Size struct{ bytes, chars, lines int }

func issue5665Measure(t *testing.T, text string) issue5665Size {
	t.Helper()
	if !utf8.ValidString(text) {
		t.Fatal("DeviceAssistantSend text is not valid UTF-8")
	}
	trimmed := strings.TrimSuffix(text, "\n")
	lines := 0
	if trimmed != "" {
		lines = strings.Count(trimmed, "\n") + 1
	}
	return issue5665Size{bytes: len([]byte(text)), chars: utf8.RuneCountInString(text), lines: lines}
}

func issue5665CompareSizes(t *testing.T, first, second string) {
	t.Helper()
	a, b := issue5665Measure(t, first), issue5665Measure(t, second)
	t.Logf("DeviceAssistantSend UTF-8 sizes: first=%d bytes/%d chars/%d lines; second=%d bytes/%d chars/%d lines",
		a.bytes, a.chars, a.lines, b.bytes, b.chars, b.lines)
	if b.bytes >= a.bytes || b.chars >= a.chars || b.lines > a.lines {
		t.Errorf("second event did not reduce its repeated payload: first=%+v second=%+v", a, b)
	}
}

func issue5665AssertReference(t *testing.T, text, todoID string) {
	t.Helper()
	if !strings.Contains(text, todoID) {
		t.Errorf("event lost authoritative task identity %q", todoID)
	}
	// A fresh round names todo_get; the ordinary form has the equivalent authoritative hub task link.
	if !strings.Contains(text, "todo_get") && !strings.Contains(text, todoLink(issue5665Web, todoID)) {
		t.Errorf("event has no authoritative retrieval reference for %s", todoID)
	}
}

func issue5665AssertAuthoritativeSources(t *testing.T, fx *issue5665Fixture, s *Service) {
	t.Helper()
	brief, ok := s.briefOfTodo(fx.todo.ID)
	if !ok || brief.Request == nil || brief.Request.Text != issue5665Request {
		t.Fatalf("original request is no longer authoritatively retrievable: %+v", brief)
	}
	current := todoNow(s, fx.todo.ID)
	if current.Supervision == nil || len(current.Supervision.DoneWhen) == 0 || current.Supervision.DoneWhen[0] != issue5665Criterion {
		t.Fatalf("acceptance criteria are no longer authoritative: %+v", current.Supervision)
	}
	got, err := runJSON(t, s, "conv1", "todo_get", map[string]any{"id": fx.todo.ID})
	if err != nil || !strings.Contains(got, issue5665Criterion) || !strings.Contains(got, issue5665Evidence) {
		t.Fatalf("todo_get cannot retrieve acceptance criteria/evidence: %q, %v", got, err)
	}
	got, err = runJSON(t, s, "conv1", "agent_read", map[string]any{"machine": "box", "session_id": issue5665AgentID, "turns": 1})
	if err != nil || !issue5665Contains(got, issue5665AgentLast) {
		t.Fatalf("agent_read cannot retrieve the unchanged last answer: %q, %v", got, err)
	}
}

func TestIssue5665InterruptedAndOrphanedEventsAtSendBoundary(t *testing.T) {
	t.Run("interrupted", func(t *testing.T) {
		s, f, todo := newSupervisedService(t)
		s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
		ctx := context.Background()
		f.setSessions("box", proto.Session{SessionID: "s1", MachineID: "box", Agent: "claude", Status: proto.StatusInterrupted,
			State: proto.StateUnread, LastText: issue5665FirstReason, UpdatedAt: time.Now()})
		s.taskNotice(ctx, todo, "box", "interrupted", "", issue5665FirstReason)
		g := issue5665Pending(t, s, todo.ID+"|"+sigInterrupted)
		text := issue5665Text(issue5665Deliver(t, s, f, "conv1", g, true))
		for _, want := range []string{todo.ID, todo.Title, "signal=" + sigInterrupted, issue5665FirstReason, "被模型服务出错打断了"} {
			if !strings.Contains(text, want) {
				t.Errorf("interrupted event lacks %q:\n%s", want, text)
			}
		}
		issue5665AssertReference(t, text, todo.ID)
		for _, manual := range []string{"## Task supervision", "Own supervised tasks' completion", "`interrupted`: continue or retry", "### Review and communication"} {
			if strings.Contains(text, manual) {
				t.Errorf("runtime-bound interrupted event contains general manual %q", manual)
			}
		}
		policy := prompts.RenderSupervisionInstructions()
		if !strings.Contains(policy, "`interrupted`: continue or retry when useful") {
			t.Fatal("stable policy lost the interrupted event's next action")
		}
	})

	t.Run("orphaned", func(t *testing.T) {
		s, f := newQuietService(t)
		s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
		todo := orphanTask(t, s)
		now := time.Now()
		agent := closedAgent(now.Add(-time.Hour))
		agent.TurnError = "API Error: 400 synthetic context overflow"
		agent.LastText = agent.TurnError
		f.setSessions("box", agent)
		ctx := context.Background()
		s.orphanWatch(ctx, now)
		g, ok := s.orphanSignal(todoNow(s, todo.ID))
		if !ok {
			t.Fatal("synthetic closed Agent did not produce an orphaned event")
		}
		text := issue5665Text(issue5665Deliver(t, s, f, "conv1", g, true))
		for _, want := range []string{todo.ID, todo.Title, "signal=" + sigOrphaned, agent.TurnError, "没有 Agent 在做了", "被模型服务报错打断"} {
			if !strings.Contains(text, want) {
				t.Errorf("orphaned event lacks %q:\n%s", want, text)
			}
		}
		issue5665AssertReference(t, text, todo.ID)
		for _, manual := range []string{"## Task supervision", "Own supervised tasks' completion", "`orphaned`: the task is open", "### Review and communication"} {
			if strings.Contains(text, manual) {
				t.Errorf("runtime-bound orphaned event contains general manual %q", manual)
			}
		}
		policy := prompts.RenderSupervisionInstructions()
		if !strings.Contains(policy, "`orphaned`: the task is open but nobody resumes it automatically") || !strings.Contains(policy, "resume with `agent_send`") {
			t.Fatal("stable policy lost the orphaned event's next-action access")
		}
	})
}

func TestIssue5665RepeatedTaskFactsAreDeltasAtSendBoundary(t *testing.T) {
	for _, mode := range []string{"continued_resume", "manager_restart", "fresh_round"} {
		t.Run(mode, func(t *testing.T) {
			fx := newIssue5665Fixture(t)
			to := "conv1"
			if mode == "fresh_round" {
				to = withBackgroundBG1(t, fx.s, fx.f)
			}
			first := superviseSignal{Todo: fx.todo.ID, Kind: sigInterrupted, Key: "issue-5665-first", Detail: issue5665FirstReason,
				Machine: "box", Session: issue5665AgentID, At: time.Now()}
			firstSend := issue5665Deliver(t, fx.s, fx.f, to, first, true)
			if mode == "fresh_round" && firstSend[superviseRoundParam] != true {
				t.Fatal("fixture did not exercise a fresh supervision round")
			}
			firstText := issue5665Text(firstSend)
			for _, want := range []string{issue5665Criterion, issue5665Request, issue5665Evidence, issue5665AgentLast, issue5665FirstReason} {
				if !issue5665Contains(firstText, want) {
					t.Fatalf("first-event fixture lacks %q:\n%s", want, firstText)
				}
			}
			issue5665AssertReference(t, firstText, fx.todo.ID)

			s, f := fx.s, fx.f
			if mode == "manager_restart" {
				settleService(t, fx.s)
				s, f = newQuietServiceOn(t, fx.st)
				s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
				// The fixture store persists settings and plans but not the to-do tables.
				// Restore the same rows so this exercises the durable supervision history.
				fx.s.todos.mu.RLock()
				s.todos.todos = make(map[string]*Todo, len(fx.s.todos.todos))
				for id, row := range fx.s.todos.todos {
					cloned := copyTodoLocked(row)
					s.todos.todos[id] = &cloned
				}
				s.todos.loaded = true
				fx.s.todos.mu.RUnlock()
				fx.s.tbriefs.mu.Lock()
				s.tbriefs.briefs = make(map[string]*taskBrief, len(fx.s.tbriefs.briefs))
				for id, brief := range fx.s.tbriefs.briefs {
					cloned := *brief
					s.tbriefs.briefs[id] = &cloned
				}
				s.tbriefs.links = make(map[string]taskBriefLink, len(fx.s.tbriefs.links))
				for id, link := range fx.s.tbriefs.links {
					s.tbriefs.links[id] = link
				}
				fx.s.tbriefs.mu.Unlock()
				fx.agent(f)
			}
			second := superviseSignal{Todo: fx.todo.ID, Kind: sigIdle, Key: "issue-5665-second", Detail: issue5665NextReason,
				Machine: "box", Session: issue5665AgentID, At: time.Now().Add(time.Minute)}
			secondText := issue5665Text(issue5665Deliver(t, s, f, to, second, true))
			for _, want := range []string{fx.todo.ID, "signal=" + sigIdle, issue5665NextReason} {
				if !strings.Contains(secondText, want) {
					t.Errorf("changed event fact %q was not delivered:\n%s", want, secondText)
				}
			}
			for label, unchanged := range map[string]string{
				"acceptance criteria": issue5665Criterion,
				"original request":    issue5665Request,
				"evidence":            issue5665Evidence,
				"Agent last message":  issue5665AgentLast,
			} {
				if issue5665Contains(secondText, unchanged) {
					t.Errorf("second delivered event repeats unchanged %s: %q\n%s", label, unchanged, secondText)
				}
			}
			issue5665AssertReference(t, secondText, fx.todo.ID)
			issue5665CompareSizes(t, firstText, secondText)
			issue5665AssertAuthoritativeSources(t, fx, s)
		})
	}
}

func TestIssue5665ReportDeltaAndWholeSignalDedupAtSendBoundary(t *testing.T) {
	fx := newIssue5665Fixture(t)
	ctx := context.Background()
	reportAt := time.Now()
	report := func(evidence []string, at time.Time) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"kind": upReportQuestion, "text": issue5665Report, "evidence": evidence, "step": 2, "steps": 4})
		if _, err := fx.s.reportToSupervisor(ctx, "box", issue5665AgentID, raw, at); err != nil {
			t.Fatal(err)
		}
	}
	report([]string{issue5665OldEvidence}, reportAt)
	firstSignal := issue5665Pending(t, fx.s, fx.todo.ID+"|"+sigReport+":"+upReportQuestion)
	firstText := issue5665Text(issue5665Deliver(t, fx.s, fx.f, "conv1", firstSignal, true))
	for _, want := range []string{issue5665Report, issue5665OldEvidence} {
		if !issue5665Contains(firstText, want) {
			t.Fatalf("first report fixture lacks %q:\n%s", want, firstText)
		}
	}
	issue5665AssertReference(t, firstText, fx.todo.ID)
	// Retrying the identical whole signal is still a deduplicated non-delivery.
	issue5665AssertDedup(t, fx.s, fx.f, firstSignal)

	report([]string{issue5665OldEvidence, issue5665NewEvidence}, reportAt.Add(time.Minute))
	secondSignal := issue5665Pending(t, fx.s, fx.todo.ID+"|"+sigReport+":"+upReportQuestion)
	secondText := issue5665Text(issue5665Deliver(t, fx.s, fx.f, "conv1", secondSignal, true))
	if !strings.Contains(secondText, issue5665NewEvidence) {
		t.Errorf("changed report evidence was not delivered:\n%s", secondText)
	}
	for label, unchanged := range map[string]string{"report prose": issue5665Report, "old report evidence": issue5665OldEvidence} {
		if strings.Contains(secondText, unchanged) {
			t.Errorf("second delivered report repeats unchanged %s: %q\n%s", label, unchanged, secondText)
		}
	}
	issue5665AssertReference(t, secondText, fx.todo.ID)
	issue5665CompareSizes(t, firstText, secondText)
	// Delta rendering must not weaken the existing whole-signal deduplication contract.
	issue5665AssertDedup(t, fx.s, fx.f, secondSignal)
	progress := todoNow(fx.s, fx.todo.ID).Supervision.Progress
	if progress == nil || progress.Detail != issue5665Report {
		t.Fatalf("authoritative full report was lost while rendering a delta: %+v", progress)
	}
	issue5665AssertAuthoritativeSources(t, fx, fx.s)
}
