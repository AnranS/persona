package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// An agent on a reporting conversation's open todo reports back to that conversation while agent tasks are on. The
// report keeps the conversation's existing reporter destination instead of becoming a main-conversation supervision
// message. With agent tasks off, the old no-receiver answer and absence of report side effects are unchanged.
func TestReportFromAReportingTodoReachesItsReportingConversation(t *testing.T) {
	ctx := context.Background()
	report := json.RawMessage(`{"kind":"done","text":"MR 提好了","evidence":["https://example.com/mr/7"]}`)

	t.Run("agent tasks off", func(t *testing.T) {
		s, f := reportingWithTask(t)
		todo := onlyTodo(s)
		if todo.Origin != "lark1" || todo.DoneAt != nil || todo.Supervision != nil {
			t.Fatalf("reporting conversation's todo = %+v, supervision %+v", todo, todo.Supervision)
		}
		res := s.agentTool(ctx, "box", "s1", "report_to_supervisor", report)
		if res.IsError || !strings.Contains(res.Content[0].Text, "Nobody receives this") {
			t.Fatalf("agent_tasks off report: %+v", res)
		}
		if got := todoNow(s, todo.ID); got.Supervision != nil {
			t.Fatalf("agent_tasks off made a supervision ledger: %+v", got.Supervision)
		}
		if kinds := feedKinds(s, "lark1"); strings.Contains(kinds, "report") {
			t.Fatalf("agent_tasks off added a report to the reporting conversation's feed: %q", kinds)
		}
		if kinds := feedKinds(s, "conv1"); strings.Contains(kinds, "report") {
			t.Fatalf("agent_tasks off added a report to the main conversation's feed: %q", kinds)
		}
		if got := f.noticesTo("lark1"); len(got) != 0 {
			t.Fatalf("agent_tasks off sent a supervision notice to the reporting conversation: %q", got)
		}
		if got := f.notices(); len(got) != 0 {
			t.Fatalf("agent_tasks off sent a supervision notice to the main conversation: %q", got)
		}
	})

	t.Run("agent tasks on", func(t *testing.T) {
		s, f := reportingWithTask(t)
		agentTasksOn(t, s)
		// An idle, active reporting conversation can receive its notice immediately.
		f.setSessions("server",
			proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
			proto.Session{SessionID: "lark1", MachineID: "server", Assistant: true, Status: proto.StatusIdle})
		todo := onlyTodo(s)
		if todo.Origin != "lark1" || todo.DoneAt != nil || todo.Supervision != nil {
			t.Fatalf("reporting conversation's todo = %+v, supervision %+v", todo, todo.Supervision)
		}
		res := s.agentTool(ctx, "box", "s1", "report_to_supervisor", report)
		if res.IsError || !strings.Contains(res.Content[0].Text, "Sent to your supervisor") {
			t.Fatalf("the reporting conversation's open todo has no report receiver: %+v", res)
		}
		waitFor(t, "the reporting conversation's supervision notice", func() bool {
			return strings.Contains(strings.Join(f.noticesTo("lark1"), "\n"), "MR 提好了")
		})
		if n := strings.Join(f.noticesTo("lark1"), "\n"); !strings.Contains(n, "📋") ||
			!strings.Contains(n, "MR 提好了") || !strings.Contains(n, "https://example.com/mr/7") {
			t.Fatalf("reporting conversation's supervision notice:\n%s", n)
		}
		if kinds := feedKinds(s, "lark1"); !strings.Contains(kinds, "report") {
			t.Fatalf("the reporting conversation's feed has no report: %q", kinds)
		}
		if got := f.notices(); len(got) != 0 {
			t.Fatalf("the report was delivered to the main conversation too: %q", got)
		}
		if kinds := feedKinds(s, "conv1"); strings.Contains(kinds, "report") {
			t.Fatalf("the main conversation's feed received the report: %q", kinds)
		}
	})
}
