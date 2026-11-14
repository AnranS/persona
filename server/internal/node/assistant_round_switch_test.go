package node

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"testing"
	"time"
)

// A round's process (#7315, assistant_round.go) has the round's tools only and is never compacted when idle. Every
// message that is no round from the first turn needs every tool: the background supervisor's once
// assistant.background_supervisor_keeps_context is on (no supervise_round), the owner's own, and any of a manager from
// before #7315. The idle round's process is replaced by a plain resume with every tool, compacted when idle again; a
// busy one takes the message and is replaced at the first message after it is idle.

// roundThenIdle runs a round from the first turn and ends its turn: the conversation's process is the round's.
func roundThenIdle(t *testing.T, n *Node) *assistantProc {
	t.Helper()
	if out := sendRound(t, n, "once-round"); out["context"] != roundFresh {
		t.Fatalf("the round: %v", out)
	}
	p := idleRound(n)
	if p == nil || !p.round || p.toolSet != roundToolSet {
		t.Fatalf("the round's process: %+v", p)
	}
	return p
}

// sendAs is an assistant.send with params, and the node's answer.
func sendAs(t *testing.T, n *Node, params map[string]any) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(params)
	out, err := n.handle(context.Background(), "assistant.send", raw)
	if err != nil {
		t.Fatal(err)
	}
	return out.(map[string]any)
}

// supervisionWithoutRound is the background supervisor's supervision message as the manager sends it with the switch
// on: what it sent before #7315 too, with no supervise_round.
func supervisionWithoutRound(onceID string) map[string]any {
	return map[string]any{"session_id": roundConversation, "text": "📋 督办：一个任务要你处理", "message_source": map[string]any{"kind": "supervision"},
		"resume_only": true, "hold_pushes": true, "once_id": onceID}
}

// everyToolStart fails t unless args are a plain resume of the conversation with every tool: the assistant's
// built-ins, no --strict-mcp-config, and Persona's MCP server told no tool set.
func everyToolStart(t *testing.T, args []string) {
	t.Helper()
	if argAfter(args, "--resume") != roundConversation || hasArg(args, "--resume-session-at") || argAfter(args, "--tools") != assistantBuiltinTools || hasArg(args, "--strict-mcp-config") {
		t.Fatalf("started as %q", args)
	}
	var mcp struct {
		Servers map[string]struct {
			Env map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(argAfter(args, "--mcp-config")), &mcp); err != nil || len(mcp.Servers) == 0 {
		t.Fatalf("its MCP servers: %v %s", err, argAfter(args, "--mcp-config"))
	}
	for name, s := range mcp.Servers {
		if set, ok := s.Env["PERSONA_TOOL_SET"]; ok {
			t.Fatalf("server %s is told tool set %q", name, set)
		}
	}
}

// compactsWhenIdle fails t unless p is out of round state and, idle with a long context, is compacted before it is let
// go (reapAssistants: idleCompactable, then compactIdle).
func compactsWhenIdle(t *testing.T, n *Node, p *assistantProc, input string) {
	t.Helper()
	p.mu.Lock()
	p.context = assistantIdleCompact * 2
	compactable := p.idleCompactable()
	p.mu.Unlock()
	if p.round || p.toolSet != "" || !compactable {
		t.Fatalf("round %v, tool set %q, compactable %v", p.round, p.toolSet, compactable)
	}
	n.compactIdle(p)
	waitFor(t, "the idle compaction", func() bool {
		msgs := roundInputs(t, input)
		return strings.Contains(msgs[len(msgs)-1], `"/compact"`)
	})
}

// The next message after a round that asks for no round (the switch turned on, or a manager from before #7315)
// replaces the idle round's process: a plain resume with every tool, no round state, compacted when idle again.
func TestRoundProcessGetsEveryToolForTheNextMessage(t *testing.T) {
	older := map[string]any{"session_id": roundConversation, "text": "📋 督办：一个任务要你处理", "resume_only": true, "once_id": "once-next"}
	explicit := supervisionWithoutRound("once-next")
	explicit["supervise_round"] = false
	for _, c := range []struct {
		name   string
		params map[string]any
	}{
		{"the switch turned on", supervisionWithoutRound("once-next")},
		{"a manager from before #7315", older},
		{"supervise_round false", explicit},
	} {
		t.Run(c.name, func(t *testing.T) {
			n, argv, input := roundTestNode(t, roundStarted("2.1.322", 2), false)
			round := roundThenIdle(t, n)
			out := sendAs(t, n, c.params)
			if out["context"] != nil || out["session_id"] != roundConversation {
				t.Fatalf("answer %v", out)
			}
			all := roundStarts(t, argv)
			if len(all) != 2 {
				t.Fatalf("%d starts: %q", len(all), all)
			}
			everyToolStart(t, all[1])
			waitFor(t, "the round's process to end", func() bool { return !round.alive() })
			if msgs := roundInputs(t, input); len(msgs) != 2 || !strings.Contains(msgs[1], "一个任务要你处理") {
				t.Fatalf("messages %q", msgs)
			}
			p := idleRound(n)
			if p == round {
				t.Fatal("the message went to the round's process")
			}
			compactsWhenIdle(t, n, p, input)
		})
	}
}

// The owner writing to the background supervisor after a round (session.send, as the app does) gets every tool the
// same way.
func TestOwnerMessageToARoundProcessGetsEveryTool(t *testing.T) {
	n, argv, input := roundTestNode(t, roundStarted("2.1.322", 1), false)
	round := roundThenIdle(t, n)
	raw, _ := json.Marshal(map[string]any{"session_id": roundConversation, "text": "the owner's question"})
	if _, err := n.handle(context.Background(), "session.send", raw); err != nil {
		t.Fatal(err)
	}
	all := roundStarts(t, argv)
	if len(all) != 2 {
		t.Fatalf("%d starts: %q", len(all), all)
	}
	everyToolStart(t, all[1])
	waitFor(t, "the round's process to end", func() bool { return !round.alive() })
	if msgs := roundInputs(t, input); len(msgs) != 2 || !strings.Contains(msgs[1], "the owner's question") {
		t.Fatalf("messages %q", msgs)
	}
	compactsWhenIdle(t, n, idleRound(n), input)
}

// A round's process that is answering, or runs background tasks, takes a message that needs every tool as it is
// (stopping it would lose its work) and says so in the log; the first message after it is idle replaces it.
func TestBusyRoundProcessSwitchesToEveryToolOnceIdle(t *testing.T) {
	n, argv, input := roundTestNode(t, roundStarted("2.1.322", 1), false)
	var logs syncBuffer
	n.logger = log.New(&logs, "", 0)
	if out := sendRound(t, n, "once-round"); out["context"] != roundFresh {
		t.Fatalf("the round: %v", out)
	}
	n.mu.Lock()
	round := n.procs[roundConversation]
	n.mu.Unlock()
	// Its turn has not ended: busy.
	sendAs(t, n, supervisionWithoutRound("once-busy"))
	if got := len(roundStarts(t, argv)); got != 1 {
		t.Fatalf("restarted mid-turn (%d starts)", got)
	}
	if !strings.Contains(logs.String(), "working with a supervision round's tools") {
		t.Fatalf("the log does not say the switch waits:\n%s", logs.String())
	}
	// Idle, with a background task running.
	idleRound(n)
	round.mu.Lock()
	round.tasks = []string{"a background command"}
	round.mu.Unlock()
	sendAs(t, n, supervisionWithoutRound("once-tasks"))
	if got := len(roundStarts(t, argv)); got != 1 || !round.alive() {
		t.Fatalf("restarted with a background task (%d starts, alive %v)", got, round.alive())
	}
	n.mu.Lock()
	same := n.procs[roundConversation] == round
	n.mu.Unlock()
	// The send returns once the message is written to the process; the fake claude appends it to its file after that.
	waitFor(t, "the third message", func() bool { return len(roundInputs(t, input)) == 3 })
	if msgs := roundInputs(t, input); !same || len(msgs) != 3 || !round.round {
		t.Fatalf("the messages did not go to the round's process: same %v, round %v, %q", same, round.round, msgs)
	}
	// Idle, nothing running: the next message replaces it.
	idleRound(n)
	round.mu.Lock()
	round.tasks = nil
	round.mu.Unlock()
	sendAs(t, n, supervisionWithoutRound("once-idle"))
	all := roundStarts(t, argv)
	if len(all) != 2 {
		t.Fatalf("%d starts: %q", len(all), all)
	}
	everyToolStart(t, all[1])
	waitFor(t, "the round's process to end", func() bool { return !round.alive() })
	compactsWhenIdle(t, n, idleRound(n), input)
}

// roundProc is the conversation's process.
func roundProc(n *Node) *assistantProc {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.procs[roundConversation]
}

// feedAssistant hands p lines as if its claude printed them (readAssistant), and returns once they are read.
func feedAssistant(n *Node, p *assistantProc, lines ...string) {
	n.readAssistant(p, strings.NewReader(strings.Join(lines, "\n")+"\n"))
}

// What Claude Code prints at a turn's end, when a compaction is done, and when its background tasks change.
const (
	turnResult     = `{"type":"result","subtype":"success","is_error":false,"num_turns":1,"result":"好"}`
	compactedLine  = `{"type":"system","subtype":"compact_boundary","compact_metadata":{"post_tokens":1000}}`
	oneTaskLine    = `{"type":"system","subtype":"background_tasks_changed","tasks":[{"description":"a background command"}]}`
	noTaskLine     = `{"type":"system","subtype":"background_tasks_changed","tasks":[]}`
	lifecycleShape = `{"type":"command_lifecycle","command_uuid":%q,"state":%q}`
)

// ownerSends is the owner writing text to the conversation, as the app does (session.send).
func ownerSends(t *testing.T, n *Node, text string) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"session_id": roundConversation, "text": text})
	if _, err := n.handle(context.Background(), "session.send", raw); err != nil {
		t.Fatal(err)
	}
}

// joinedState is p's state as the reaper reads it once p's turns ended: idle, reapable a while later, and whether it is
// compacted before it is let go with a long context.
func joinedState(p *assistantProc) (idle, reapable, compactable bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.context = assistantIdleCompact * 2
	return !p.workingLocked(), p.reapable(time.Now().Add(assistantIdleTimeout + time.Minute)), p.idleCompactable()
}

// A message that needs every tool and joins a round's process still working ends its exemption from idle compaction
// there and then: once its turns end, with no other message, the reaper compacts it before letting it go as any other
// process, also when what kept it working was a background task that ends later. The tools switch with the next
// message, which is the first thing that starts a turn in it again. A round kept as busy is the round's own: it keeps
// the exemption, and the next round starts from the first turn.
func TestJoinedRoundProcessIsCompactedWhenIdle(t *testing.T) {
	for _, c := range []struct {
		name   string
		tasks  bool // a background task, not the round's turn, keeps the process working
		join   func(t *testing.T, n *Node)
		joined bool
	}{
		{"a supervision message with the switch on", false, func(t *testing.T, n *Node) { sendAs(t, n, supervisionWithoutRound("once-join")) }, true},
		{"the owner's message", false, func(t *testing.T, n *Node) { ownerSends(t, n, "the owner's question") }, true},
		{"the owner's message beside a background task", true, func(t *testing.T, n *Node) { ownerSends(t, n, "the owner's question") }, true},
		{"a round kept as busy", false, func(t *testing.T, n *Node) {
			if out := sendRound(t, n, "once-kept"); out["context"] != roundKept || out["why"] != roundWhyBusy {
				t.Fatalf("the round kept: %v", out)
			}
		}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			n, argv, input := roundTestNode(t, roundStarted("2.1.322", 1), false)
			if out := sendRound(t, n, "once-round"); out["context"] != roundFresh {
				t.Fatalf("the round: %v", out)
			}
			round := roundProc(n)
			if c.tasks {
				feedAssistant(n, round, oneTaskLine, turnResult) // the round's turn ended, its background command runs on
			}
			c.join(t, n)
			waitFor(t, "the joining message", func() bool { return len(roundInputs(t, input)) == 2 })
			feedAssistant(n, round, turnResult) // its turn ends
			if c.tasks {
				if idle, reapable, _ := joinedState(round); idle || reapable {
					t.Fatalf("with its background command: idle %v, reapable %v", idle, reapable)
				}
				feedAssistant(n, round, noTaskLine)
			}
			if got := len(roundStarts(t, argv)); got != 1 || !round.alive() || roundProc(n) != round {
				t.Fatalf("replaced while working (%d starts, alive %v)", got, round.alive())
			}
			idle, reapable, compactable := joinedState(round)
			if !idle || !reapable || compactable != c.joined {
				t.Fatalf("idle %v, reapable %v, compactable %v (want %v)", idle, reapable, compactable, c.joined)
			}
			if !c.joined {
				// The next round starts from the first turn, its exemption intact.
				if out := sendRound(t, n, "once-next"); out["context"] != roundFresh {
					t.Fatalf("the next round: %v", out)
				}
				return
			}
			n.compactIdle(round)
			waitFor(t, "the idle compaction", func() bool {
				msgs := roundInputs(t, input)
				return strings.Contains(msgs[len(msgs)-1], `"/compact"`)
			})
			// A message while it compacts goes to it (a new process would lose the compaction); once it is done, the next
			// message gets every tool.
			sendAs(t, n, supervisionWithoutRound("once-compacting"))
			if got := len(roundStarts(t, argv)); got != 1 || !round.alive() {
				t.Fatalf("replaced while compacting (%d starts, alive %v)", got, round.alive())
			}
			feedAssistant(n, round, compactedLine, turnResult, turnResult)
			sendAs(t, n, supervisionWithoutRound("once-after"))
			all := roundStarts(t, argv)
			if len(all) != 2 {
				t.Fatalf("%d starts: %q", len(all), all)
			}
			everyToolStart(t, all[1])
			waitFor(t, "the round's process to end", func() bool { return !round.alive() })
		})
	}
}

// The user compacting the background supervisor while a round's process is up sets no busy flag (assistantCompact):
// a message that needs every tool goes into that process, as a round does (kept as busy), and the switch waits for the
// compaction to end. Replacing the process then would end the compaction unfinished.
func TestRoundProcessIsNotReplacedWhileTheUserCompacts(t *testing.T) {
	for _, c := range []struct {
		name string
		send func(t *testing.T, n *Node)
	}{
		{"a supervision message with the switch on", func(t *testing.T, n *Node) { sendAs(t, n, supervisionWithoutRound("once-during")) }},
		{"the owner's message", func(t *testing.T, n *Node) { ownerSends(t, n, "the owner's question") }},
		{"a round", func(t *testing.T, n *Node) {
			if out := sendRound(t, n, "once-during"); out["context"] != roundKept || out["why"] != roundWhyBusy {
				t.Fatalf("the round during the compaction: %v", out)
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			n, argv, input := roundTestNode(t, roundStarted("2.1.322", 1), false)
			round := roundThenIdle(t, n)
			if err := n.assistantCompact(context.Background(), roundConversation); err != nil {
				t.Fatal(err)
			}
			c.send(t, n)
			if got := len(roundStarts(t, argv)); got != 1 || !round.alive() || roundProc(n) != round {
				t.Fatalf("replaced while the user compacts (%d starts, alive %v)", got, round.alive())
			}
			waitFor(t, "the message after the compaction's", func() bool { return len(roundInputs(t, input)) == 3 })
			// The compaction ends, and the message's turn: the next message gets every tool.
			feedAssistant(n, round, compactedLine, turnResult, turnResult)
			sendAs(t, n, supervisionWithoutRound("once-after"))
			all := roundStarts(t, argv)
			if len(all) != 2 {
				t.Fatalf("%d starts: %q", len(all), all)
			}
			everyToolStart(t, all[1])
		})
	}
}

// A message Claude Code queued behind the round's turn has not started when that turn's result clears busy: a new
// process would never get it. Until it starts, a message that needs every tool goes into the round's process and a round
// is kept as busy; once it started and every turn ended, the next message gets every tool.
func TestRoundProcessKeepsAMessageClaudeQueued(t *testing.T) {
	for _, c := range []struct {
		name string
		send func(t *testing.T, n *Node)
	}{
		{"a supervision message with the switch on", func(t *testing.T, n *Node) { sendAs(t, n, supervisionWithoutRound("once-gap")) }},
		{"the owner's message", func(t *testing.T, n *Node) { ownerSends(t, n, "and one more") }},
		{"a round", func(t *testing.T, n *Node) {
			if out := sendRound(t, n, "once-gap"); out["context"] != roundKept || out["why"] != roundWhyBusy {
				t.Fatalf("the round in the gap: %v", out)
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			n, argv, input := roundTestNode(t, roundStarted("2.1.322", 1), false)
			if out := sendRound(t, n, "once-round"); out["context"] != roundFresh {
				t.Fatalf("the round: %v", out)
			}
			round := roundProc(n)
			ownerSends(t, n, "the owner's question") // the round's turn has not ended: busy
			round.mu.Lock()
			owner := round.written[len(round.written)-1].id
			round.mu.Unlock()
			// Claude Code queues it, then the round's turn ends.
			feedAssistant(n, round, fmt.Sprintf(lifecycleShape, owner, "queued"), turnResult)
			round.mu.Lock()
			busy, queued := round.busy, len(round.queuedLocked())
			round.mu.Unlock()
			if busy || queued != 1 {
				t.Fatalf("busy %v, %d queued", busy, queued)
			}
			c.send(t, n)
			if got := len(roundStarts(t, argv)); got != 1 || !round.alive() || roundProc(n) != round {
				t.Fatalf("replaced with a queued message (%d starts, alive %v)", got, round.alive())
			}
			waitFor(t, "the message in the gap", func() bool { return len(roundInputs(t, input)) == 3 })
			// The queued message starts, and every turn ends.
			feedAssistant(n, round, fmt.Sprintf(lifecycleShape, owner, "started"), turnResult, turnResult)
			sendAs(t, n, supervisionWithoutRound("once-after"))
			all := roundStarts(t, argv)
			if len(all) != 2 {
				t.Fatalf("%d starts: %q", len(all), all)
			}
			everyToolStart(t, all[1])
		})
	}
}
