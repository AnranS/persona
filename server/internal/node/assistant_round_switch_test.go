package node

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"testing"
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
	if !strings.Contains(logs.String(), "busy with a supervision round's tools") {
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
