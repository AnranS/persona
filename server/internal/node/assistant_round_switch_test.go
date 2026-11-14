package node

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"slices"
	"strings"
	"testing"
	"time"
)

// A round's process (#7315, assistant_round.go) has the round's tools only and is never compacted when idle. Every
// message that is no round from the first turn needs every tool: the background supervisor's once
// assistant.background_supervisor_keeps_context is on (no supervise_round), the owner's own, and any of a manager from
// before #7315. The idle round's process is replaced by a plain resume with every tool, compacted when idle again. While
// it works, the node holds such a message, and once it is idle the first held message replaces it and the others
// follow, in order.

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

// What Claude Code 2.1.322 prints at a turn's end, when a compaction is done, when its background tasks change, when
// one of them ended (before it takes its notification up in a turn of its own), and as every turn after its first
// starts.
const (
	turnResult       = `{"type":"result","subtype":"success","is_error":false,"num_turns":1,"result":"好"}`
	compactedLine    = `{"type":"system","subtype":"compact_boundary","compact_metadata":{"post_tokens":1000}}`
	oneTaskLine      = `{"type":"system","subtype":"background_tasks_changed","tasks":[{"description":"a background command"}]}`
	noTaskLine       = `{"type":"system","subtype":"background_tasks_changed","tasks":[]}`
	taskEndedLine    = `{"type":"system","subtype":"task_notification","task_id":"b1","status":"completed","summary":"Background command \"a background command\" completed (exit code 0)"}`
	turnInitLine     = `{"type":"system","subtype":"init","claude_code_version":"2.1.322","tools":["Bash"]}`
	lifecycleShape   = `{"type":"command_lifecycle","command_uuid":%q,"state":%q}`
	messageStartLine = `{"type":"stream_event","event":{"type":"message_start","message":{"model":"m","usage":{"input_tokens":10}}}}`
)

// ownerSends is the owner writing text to the conversation, as the app does (session.send).
func ownerSends(t *testing.T, n *Node, text string) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"session_id": roundConversation, "text": text})
	if _, err := n.handle(context.Background(), "session.send", raw); err != nil {
		t.Fatal(err)
	}
}

// relay is the manager's supervision message once with the switch on, its words naming once so the test can find it.
func relay(once string) map[string]any {
	params := supervisionWithoutRound(once)
	params["text"] = "supervision message " + once
	return params
}

// workingNow is whether stopping p now would lose something (workingLocked), and its row's status.
func workingNow(n *Node, p *assistantProc) (bool, string) {
	p.mu.Lock()
	working := p.workingLocked()
	p.mu.Unlock()
	for _, s := range n.assistantSessions() {
		if s.SessionID == p.id {
			return working, s.Status
		}
	}
	return working, ""
}

// stillHeld fails t unless p is still the conversation's process, starts processes were started in all, and none of
// words reached any of them.
func stillHeld(t *testing.T, n *Node, p *assistantProc, argv, input string, starts int, words ...string) {
	t.Helper()
	if got := len(roundStarts(t, argv)); got != starts || !p.alive() || roundProc(n) != p {
		t.Fatalf("replaced (%d starts, alive %v)", got, p.alive())
	}
	read := strings.Join(roundInputs(t, input), "\n")
	for _, w := range words {
		if strings.Contains(read, w) {
			t.Fatalf("%q went in while the process worked", w)
		}
	}
}

// settled waits until no delivery of p's held messages is under way: one that started when p looked idle has looked
// again after assistantHeldSettle (deliverHeld).
func settled(t *testing.T, p *assistantProc) {
	t.Helper()
	waitFor(t, "the held messages' second look", func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return !p.delivering
	})
}

// heldGoIn fails t unless the round's process p is replaced by a plain resume with every tool (the starts-th start),
// which reads words once each, in that order, and is out of round state and compacted when idle.
func heldGoIn(t *testing.T, n *Node, p *assistantProc, argv, input string, starts int, words ...string) {
	t.Helper()
	waitFor(t, "a process with every tool", func() bool { return len(roundStarts(t, argv)) == starts })
	all := roundStarts(t, argv)
	everyToolStart(t, all[starts-1])
	waitFor(t, "the round's process to end", func() bool { return !p.alive() })
	waitFor(t, "the held messages in the new process", func() bool {
		return strings.Contains(strings.Join(roundInputs(t, input), "\n"), words[len(words)-1])
	})
	msgs, at := roundInputs(t, input), -1
	for _, w := range words {
		i := slices.IndexFunc(msgs, func(m string) bool { return strings.Contains(m, w) })
		if i <= at || slices.IndexFunc(msgs[i+1:], func(m string) bool { return strings.Contains(m, w) }) >= 0 {
			t.Fatalf("%q not read once, in its order: %q", w, msgs)
		}
		at = i
	}
	np := idleRound(n)
	if np == nil || np == p {
		t.Fatal("the held messages went to the round's process")
	}
	compactsWhenIdle(t, n, np, input)
}

// holdBoth sends the first message after the switch (a supervision message without supervise_round) and the owner's,
// both needing every tool, to the round's process p at work: neither goes in, and the relay's send is answered as sent.
func holdBoth(t *testing.T, n *Node, p *assistantProc, argv, input string, starts int) {
	t.Helper()
	if out := sendAs(t, n, relay("once-held")); out["session_id"] != roundConversation || out["context"] != nil {
		t.Fatalf("the relay's answer: %v", out)
	}
	ownerSends(t, n, "the owner's question")
	stillHeld(t, n, p, argv, input, starts, "once-held", "the owner's question")
	p.mu.Lock()
	held := len(p.held)
	p.mu.Unlock()
	if held != 2 {
		t.Fatalf("%d messages held", held)
	}
}

// Turning the switch on while a round's process works: the first supervision message after it, and the owner's, are
// held at the node, outside Claude Code's queue, while the process answers, runs a background task (and the turn
// Claude Code starts on its own to take its notification up), or compacts as the user asked. None of that work is cut;
// once it is done, and the process stayed idle for assistantHeldSettle, the first held message replaces it with a plain
// resume with every tool, out of round state and compacted when idle, and the other follows, each read once.
func TestRoundProcessHoldsMessagesUntilIdle(t *testing.T) {
	for _, c := range []struct {
		name string
		work func(t *testing.T, n *Node, round *assistantProc) // makes the round's process work, before both are sent
		done func(t *testing.T, n *Node, round *assistantProc) // ends that work
	}{
		{"answering", func(*testing.T, *Node, *assistantProc) {}, func(t *testing.T, n *Node, round *assistantProc) {
			feedAssistant(n, round, turnResult)
		}},
		{"a background task, then the turn Claude Code starts for it", func(t *testing.T, n *Node, round *assistantProc) {
			feedAssistant(n, round, oneTaskLine, turnResult) // the round's turn ended, its background command runs on
		}, func(t *testing.T, n *Node, round *assistantProc) {
			// The command ends: Claude Code empties its list, which looks idle, and 26 ms later starts a turn to take
			// its notification up; its init is all that shows it until the model's first token (measured on 2.1.322).
			feedAssistant(n, round, taskEndedLine, noTaskLine, turnInitLine)
			settled(t, round)
			if working, status := workingNow(n, round); !working || status != "working" {
				t.Fatalf("the turn Claude Code started: working %v, status %q", working, status)
			}
			if !round.alive() || roundProc(n) != round {
				t.Fatal("the turn Claude Code started was cut off")
			}
			feedAssistant(n, round, messageStartLine, turnResult)
		}},
		{"the user's compaction", func(t *testing.T, n *Node, round *assistantProc) {
			feedAssistant(n, round, turnResult)
			if err := n.assistantCompact(context.Background(), roundConversation); err != nil {
				t.Fatal(err)
			}
		}, func(t *testing.T, n *Node, round *assistantProc) {
			feedAssistant(n, round, turnInitLine, compactedLine, turnResult) // a compaction prints its init at its end
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			n, argv, input := roundTestNode(t, roundStarted("2.1.322", 1), false)
			var logs syncBuffer
			n.logger = log.New(&logs, "", 0)
			if out := sendRound(t, n, "once-round"); out["context"] != roundFresh {
				t.Fatalf("the round: %v", out)
			}
			round := roundProc(n)
			c.work(t, n, round)
			holdBoth(t, n, round, argv, input, 1)
			before := time.Now()
			c.done(t, n, round)
			heldGoIn(t, n, round, argv, input, 2, "once-held", "the owner's question")
			// Not before the process stayed idle a while, well past the 26 ms Claude Code took at most to show a turn
			// of its own (its init).
			if np := roundProc(n); np.born.Sub(before) < 100*time.Millisecond {
				t.Fatalf("replaced %s after its work ended", np.born.Sub(before))
			}
			if got := strings.Count(logs.String(), "the message waits until it is idle"); got != 1 {
				t.Fatalf("the hold logged %d times:\n%s", got, logs.String())
			}
		})
	}
}

// A turn Claude Code has started (command_lifecycle started) is under way though the model has not answered yet
// (measured on 2.1.322: started 3 ms after the turn before it ended, the first token seconds later): nothing replaces
// the process under it, and nothing it was given is lost.
func TestAStartedTurnIsNotCut(t *testing.T) {
	// startedIn writes text into p as a message Claude Code queues behind the turn running, ends that turn and starts
	// the message's turn, which stops before the model's first token.
	startedIn := func(t *testing.T, n *Node, p *assistantProc, send func()) {
		t.Helper()
		send()
		p.mu.Lock()
		id := p.written[len(p.written)-1].id
		p.mu.Unlock()
		feedAssistant(n, p, fmt.Sprintf(lifecycleShape, id, "queued"), turnResult, fmt.Sprintf(lifecycleShape, id, "started"))
		if working, status := workingNow(n, p); !working || status != "working" {
			t.Fatalf("the started turn: working %v, status %q", working, status)
		}
	}
	t.Run("a round's, then the switch", func(t *testing.T) {
		n, argv, input := roundTestNode(t, roundStarted("2.1.322", 1), false)
		if out := sendRound(t, n, "once-round"); out["context"] != roundFresh {
			t.Fatalf("the round: %v", out)
		}
		round := roundProc(n)
		startedIn(t, n, round, func() { // a round while the round answers goes in as before, the round's own
			if out := sendRound(t, n, "once-kept"); out["context"] != roundKept || out["why"] != roundWhyBusy {
				t.Fatalf("the round kept: %v", out)
			}
		})
		holdBoth(t, n, round, argv, input, 1)
		settled(t, round)
		stillHeld(t, n, round, argv, input, 1, "once-held", "the owner's question")
		feedAssistant(n, round, turnInitLine, messageStartLine, turnResult)
		heldGoIn(t, n, round, argv, input, 2, "once-held", "the owner's question")
		if msgs := roundInputs(t, input); strings.Count(strings.Join(msgs, "\n"), "一个任务要你处理") != 2 {
			t.Fatalf("the two rounds were not read once each: %q", msgs)
		}
	})
	t.Run("a round's, taken into the turn running", func(t *testing.T) {
		// Claude Code takes a message queued mid-turn into that turn at its next step: started while busy, and the
		// turn's one result ends both. Nothing of it is left to keep the process working afterwards.
		n, argv, input := roundTestNode(t, roundStarted("2.1.322", 1), false)
		if out := sendRound(t, n, "once-round"); out["context"] != roundFresh {
			t.Fatalf("the round: %v", out)
		}
		round := roundProc(n)
		if out := sendRound(t, n, "once-kept"); out["context"] != roundKept || out["why"] != roundWhyBusy {
			t.Fatalf("the round kept: %v", out)
		}
		round.mu.Lock()
		id := round.written[len(round.written)-1].id
		round.mu.Unlock()
		feedAssistant(n, round, fmt.Sprintf(lifecycleShape, id, "queued"), fmt.Sprintf(lifecycleShape, id, "started"))
		holdBoth(t, n, round, argv, input, 1)
		feedAssistant(n, round, turnResult, fmt.Sprintf(lifecycleShape, id, "completed"))
		heldGoIn(t, n, round, argv, input, 2, "once-held", "the owner's question")
	})
	t.Run("the owner's, then a round", func(t *testing.T) {
		n, argv, input := roundTestNode(t, roundStarted("2.1.322", 1), false)
		if _, err := n.assistantSend(context.Background(), roundConversation, "", "", "the owner's first", nil, "", ""); err != nil {
			t.Fatal(err)
		}
		p := roundProc(n)
		startedIn(t, n, p, func() { ownerSends(t, n, "the owner's second") })
		// A round now would resume at the first turn and leave the owner's second message off the chain it goes on.
		if out := sendRound(t, n, "once-gap"); out["context"] != roundKept || out["why"] != roundWhyBusy {
			t.Fatalf("the round in the gap: %v", out)
		}
		stillHeld(t, n, p, argv, input, 1)
		waitFor(t, "the round in the process", func() bool { return len(roundInputs(t, input)) == 3 })
		feedAssistant(n, p, turnInitLine, messageStartLine, turnResult, turnInitLine, messageStartLine, turnResult)
		if out := sendRound(t, n, "once-next"); out["context"] != roundFresh {
			t.Fatalf("the round once idle: %v", out)
		}
		if msgs := roundInputs(t, input); len(msgs) != 4 || !strings.Contains(msgs[0], "the owner's first") || !strings.Contains(msgs[1], "the owner's second") {
			t.Fatalf("messages %q", msgs)
		}
	})
}

// The user compacting the background supervisor while a round's process is up sets no busy flag (assistantCompact): a
// round goes into that process (kept as busy), since replacing it would end the compaction unfinished; the next
// message after both gets every tool.
func TestRoundProcessIsNotReplacedWhileTheUserCompacts(t *testing.T) {
	n, argv, input := roundTestNode(t, roundStarted("2.1.322", 1), false)
	round := roundThenIdle(t, n)
	if err := n.assistantCompact(context.Background(), roundConversation); err != nil {
		t.Fatal(err)
	}
	if out := sendRound(t, n, "once-during"); out["context"] != roundKept || out["why"] != roundWhyBusy {
		t.Fatalf("the round during the compaction: %v", out)
	}
	stillHeld(t, n, round, argv, input, 1)
	waitFor(t, "the round after the compaction's /compact", func() bool { return len(roundInputs(t, input)) == 3 })
	feedAssistant(n, round, turnInitLine, compactedLine, turnResult, turnInitLine, turnResult)
	sendAs(t, n, relay("once-after"))
	heldGoIn(t, n, round, argv, input, 2, "once-after")
}

// A round's process that ends while it holds messages (it crashed, or was stopped) hands them to the next process,
// with every tool.
func TestHeldMessagesGoInWhenTheRoundProcessEnds(t *testing.T) {
	n, argv, input := roundTestNode(t, roundStarted("2.1.322", 1), false)
	if out := sendRound(t, n, "once-round"); out["context"] != roundFresh {
		t.Fatalf("the round: %v", out)
	}
	round := roundProc(n)
	holdBoth(t, n, round, argv, input, 1)
	round.stop()
	heldGoIn(t, n, round, argv, input, 2, "once-held", "the owner's question")
}

// A held message whose delivery finds the conversation's process at work again waits once more, first: on the process
// it was held by, or, when that one was replaced meanwhile (it ended, and a round started anew), on the new one, with
// everything the old one still held behind it and what the new one holds after them.
func TestHeldMessagesStayInOrder(t *testing.T) {
	texts := func(p *assistantProc) string {
		var out []string
		for _, h := range p.held {
			out = append(out, h.text)
		}
		return strings.Join(out, " ")
	}
	n := &Node{}
	old := &assistantProc{held: []heldSend{{text: "b"}, {text: "c"}}}
	cur := &assistantProc{held: []heldSend{{text: "d"}}}
	n.holdAgain(old, cur, heldSend{ctx: context.Background(), text: "a"})
	if got := texts(cur); got != "a b c d" || len(old.held) != 0 {
		t.Fatalf("moved: %q, %d left behind", got, len(old.held))
	}
	n.holdAgain(cur, cur, heldSend{ctx: context.Background(), text: "z"})
	if got := texts(cur); got != "z a b c d" {
		t.Fatalf("held again: %q", got)
	}
}

// A resident node asked to restart waits while a conversation holds messages, as it does while one answers: they live
// with its process, which the restart would end.
func TestHeldMessagesKeepTheNodeFromRestarting(t *testing.T) {
	p := &assistantProc{exited: make(chan struct{}), held: []heldSend{{text: "held"}}}
	liveAssistants.Store(p, struct{}{})
	defer liveAssistants.Delete(p)
	if !AssistantsBusy() {
		t.Fatal("a held message does not keep the node from restarting")
	}
}

// Claude Code prints an init as every turn starts, the first one in the turn of the message that started the process.
// A later one is a turn the node may not have asked for (a background task's notification): busy from then, the row
// says working, and a round goes in as before rather than stop the process under it. A compaction's init, which comes
// right before its end, is not a turn.
func TestLaterInitIsATurn(t *testing.T) {
	n, argv, _ := roundTestNode(t, roundStarted("2.1.322", 1), false)
	if _, err := n.assistantSend(context.Background(), roundConversation, "", "", "the owner's message", nil, "", ""); err != nil {
		t.Fatal(err)
	}
	p := roundProc(n)
	waitFor(t, "the first init", func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.initSeen
	})
	feedAssistant(n, p, turnResult)
	if working, status := workingNow(n, p); working || status == "working" {
		t.Fatalf("after its turn: working %v, status %q", working, status)
	}
	feedAssistant(n, p, taskEndedLine, turnInitLine)
	if working, status := workingNow(n, p); !working || status != "working" {
		t.Fatalf("a turn of its own: working %v, status %q", working, status)
	}
	if out := sendRound(t, n, "once-own-turn"); out["context"] != roundKept || out["why"] != roundWhyBusy {
		t.Fatalf("a round during that turn: %v", out)
	}
	if got := len(roundStarts(t, argv)); got != 1 || !p.alive() {
		t.Fatalf("stopped under its own turn (%d starts, alive %v)", got, p.alive())
	}
	feedAssistant(n, p, messageStartLine, turnResult, turnInitLine, turnResult)
	if working, status := workingNow(n, p); working || status == "working" {
		t.Fatalf("after that turn: working %v, status %q", working, status)
	}
	p.mu.Lock()
	p.compacting = true
	p.mu.Unlock()
	feedAssistant(n, p, turnInitLine)
	p.mu.Lock()
	busy := p.busy
	p.mu.Unlock()
	if busy {
		t.Fatal("a compaction's init made it busy")
	}
	feedAssistant(n, p, compactedLine, turnResult)
	if working, _ := workingNow(n, p); working {
		t.Fatal("working after the compaction")
	}
}
