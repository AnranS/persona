package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

// agentTasksAcceptAndRetire accepts todo through the supervisor's real review tool, then runs the existing done-task
// retirement pass with its idle wait shortened. The Agent must stay linked to the completed todo.
func agentTasksAcceptAndRetire(t *testing.T, s *Service, f *fakeHub, todo Todo) {
	t.Helper()
	ctx := context.Background()
	res, err := runJSON(t, s, "conv1", "task_review", map[string]any{
		"todo_id": todo.ID, "verdict": reviewAccepted, "note": "验收通过",
		"checks": completeReviewChecks(todoNow(s, todo.ID)),
	})
	if err != nil {
		t.Fatalf("accept %s: %v", todo.ID, err)
	}
	if !strings.Contains(res, "accepted and closed") {
		t.Fatalf("accept %s: %s", todo.ID, res)
	}
	done := todoNow(s, todo.ID)
	if done.DoneAt == nil || done.DoneBy != db.DoneByReview || done.Supervision == nil || done.Supervision.State != reviewAccepted {
		t.Fatalf("accepted todo = %+v, supervision %+v", done, done.Supervision)
	}
	if got := f.retires(); len(got) != 0 {
		t.Fatalf("the Agent retired before the done-task pass: %v", got)
	}

	old := taskCloseIdle
	taskCloseIdle = 0
	t.Cleanup(func() { taskCloseIdle = old })
	f.setSessions("box", idleAgent(time.Now().Add(-time.Hour)))
	s.closeDoneTaskAgents(ctx, taskCloseRefused{}, doneSeen{})
	if got := f.retires(); len(got) != 1 || got[0] != "s1" {
		t.Fatalf("retired Agents = %v, want [s1]", got)
	}
	f.mu.Lock()
	args := f.retireArgs[0]
	f.mu.Unlock()
	if args["method"] != deviceSessionStop || args["retire"] != true {
		t.Fatalf("retirement call = %+v", args)
	}
	if agents := todoNow(s, todo.ID).Agents; len(agents) != 1 || agents[0].SessionID != "s1" || agents[0].ReleasedAt != nil {
		t.Fatalf("the completed todo lost its Agent: %+v", agents)
	}
}

// AC1: an app-started Persona Agent is adopted, judged, accepted by its supervisor, and retired by the existing close
// path. Adoption itself leaves the supervised task open until that acceptance.
func TestAgentTasksIssue3617AppAgentAcceptedReviewRetiresAgent(t *testing.T) {
	s, f := newQuietService(t)
	agentTasksOn(t, s)
	f.judge = `{"verdict":"done","reason":"修好了"}`
	appAgent(f, true, "Read", "Edit", "Bash")
	s.taskTurnSettled(context.Background(), "box", "s1")

	todo := onlyTodo(s)
	if !todo.Quiet || todo.Origin != "conv1" || len(todo.Agents) != 1 || todo.Agents[0].SessionID != "s1" {
		t.Fatalf("adopted todo = %+v", todo)
	}
	if v := todo.Supervision; v == nil || v.Supervisor != "conv1" || len(v.DoneWhen) != 0 {
		t.Fatalf("adopted supervision = %+v", todo.Supervision)
	}
	waitFor(t, "the adopted Agent's completion review", func() bool {
		return todoNow(s, todo.ID).Supervision.state(todoNow(s, todo.ID)) == reviewPending
	})
	if got := todoNow(s, todo.ID); got.DoneAt != nil {
		t.Fatalf("the supervised todo closed without acceptance: %+v", got)
	}
	agentTasksAcceptAndRetire(t, s, f, todo)
}

// AC2: a reporting conversation's existing quiet, unsupervised task has a report receiver too. Its report returns to
// that conversation, not to the main one; with agent_tasks off the old no-receiver answer remains.
func TestAgentTasksIssue3617ReportingConversationReceivesOwnReport(t *testing.T) {
	s, f := reportingWithTask(t)
	ctx := context.Background()
	f.setSessions("server",
		proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
		proto.Session{SessionID: "lark1", MachineID: "server", Assistant: true, Status: proto.StatusIdle})
	todo := onlyTodo(s)
	if !todo.Quiet || todo.Origin != "lark1" || todo.Supervision != nil {
		t.Fatalf("reporting conversation's todo = %+v, supervision %+v", todo, todo.Supervision)
	}
	report := json.RawMessage(`{"kind":"done","text":"MR 提好了","evidence":["https://example.com/mr/7"]}`)
	if res := s.agentTool(ctx, "box", "s1", "report_to_supervisor", report); res.IsError ||
		!strings.Contains(res.Content[0].Text, "Nobody receives this") {
		t.Fatalf("agent_tasks off: %+v", res)
	}

	agentTasksOn(t, s)
	if res := s.agentTool(ctx, "box", "s1", "report_to_supervisor", report); res.IsError ||
		!strings.Contains(res.Content[0].Text, "Sent to your supervisor") {
		t.Fatalf("the reporting conversation's open todo has no report receiver: %+v", res)
	}
	waitFor(t, "the report in its own reporting conversation", func() bool {
		for _, notice := range f.noticesTo("lark1") {
			if strings.Contains(notice, "MR 提好了") && strings.Contains(notice, "https://example.com/mr/7") {
				return true
			}
		}
		return false
	})
	if !strings.Contains(feedKinds(s, "lark1"), "report") {
		t.Fatalf("the reporting conversation's feed has no report: %q", feedKinds(s, "lark1"))
	}
	for _, notice := range f.notices() {
		if strings.Contains(notice, "MR 提好了") {
			t.Fatalf("the report moved to the main conversation:\n%s", notice)
		}
	}
	if strings.Contains(feedKinds(s, "conv1"), "report") {
		t.Fatalf("the main conversation's feed received the report: %q", feedKinds(s, "conv1"))
	}
}

// AC3: linking an Agent to a user-created todo gives main a criterion-free supervision ledger without making the todo
// quiet. Its stopped turn is completion-judged into pending_review, and acceptance closes it and retires the Agent.
func TestAgentTasksIssue3617UserTodoCompletionReviewRetiresAgent(t *testing.T) {
	s, f := newQuietService(t)
	agentTasksOn(t, s)
	f.judge = `{"verdict":"done","reason":"用户待办也完成了"}`
	ctx := context.Background()
	todo, err := s.createTodo(ctx, "用户自己记的", "修好并补测试", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if w := serve(s, "PUT", "/api/v1/todos/"+todo.ID+"/agents/box/s1", ""); w.Code != 200 {
		t.Fatalf("link Agent: %d %s", w.Code, w.Body)
	}
	linked := todoNow(s, todo.ID)
	if v := linked.Supervision; v == nil || v.Supervisor != "conv1" || len(v.DoneWhen) != 0 || linked.Quiet {
		t.Fatalf("linked todo = %+v, supervision %+v", linked, linked.Supervision)
	}

	agentTurn(s, f, proto.StatusWorking, "", 0)
	agentTurn(s, f, proto.StatusDone, "修好并补了测试", 0)
	f.mu.Lock()
	judged := f.judged
	f.mu.Unlock()
	got := todoNow(s, todo.ID)
	if judged != 1 || got.Supervision.state(got) != reviewPending || got.DoneAt != nil {
		t.Fatalf("user-created todo's stopped turn: %d judgments, todo %+v, supervision %+v", judged, got, got.Supervision)
	}
	agentTasksAcceptAndRetire(t, s, f, todo)
}

// AC5 control: with the unchanged default off, linking the same kind of Agent creates no ledger and its stop causes no
// judgment or closure. report_to_supervisor keeps its old no-receiver answer and starts no new conversation routing.
func TestAgentTasksIssue3617OffLeavesUserTodoUnsupervised(t *testing.T) {
	s, f := newQuietService(t)
	f.judge = `{"verdict":"done","reason":"不应读取"}`
	ctx := context.Background()
	todo, err := s.createTodo(ctx, "用户自己记的", "修好并补测试", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if w := serve(s, "PUT", "/api/v1/todos/"+todo.ID+"/agents/box/s1", ""); w.Code != 200 {
		t.Fatalf("link Agent: %d %s", w.Code, w.Body)
	}
	agentTurn(s, f, proto.StatusWorking, "", 0)
	agentTurn(s, f, proto.StatusDone, "修好并补了测试", 0)
	f.mu.Lock()
	judged := f.judged
	f.mu.Unlock()
	if got := todoNow(s, todo.ID); got.Supervision != nil || got.DoneAt != nil || judged != 0 {
		t.Fatalf("agent_tasks off: %d judgments, todo %+v, supervision %+v", judged, got, got.Supervision)
	}
	report := json.RawMessage(`{"kind":"done","text":"完成了"}`)
	if res := s.agentTool(ctx, "box", "s1", "report_to_supervisor", report); res.IsError ||
		!strings.Contains(res.Content[0].Text, "Nobody receives this") {
		t.Fatalf("agent_tasks off report: %+v", res)
	}
	if len(f.notices()) != 0 || strings.Contains(feedKinds(s, "conv1"), "report") {
		t.Fatalf("agent_tasks off routed a report: notices %q, feed %q", f.notices(), feedKinds(s, "conv1"))
	}
}
