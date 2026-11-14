package node

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// The background supervisor's rounds (#7315, docs/assistant/unified-entry.md "Each round starts clean"). The manager
// marks a supervision message to the background supervisor as a round (assistant.send supervise_round). The node then
// stops the conversation's idle process and resumes it at the end of its first turn, `claude -p --resume <id>
// --resume-session-at <uuid>`, so the model reads the start brief and this round, not every round before it. The
// conversation id, its file and its row stay the same: Claude Code appends the round to the same file as a branch from
// that line, which the transcript reader keeps in view (transcript/branch.go), and a later plain --resume (the owner
// writing there) continues the latest round. The round's process gets only the tools supervision uses: Persona's own
// MCP server, told PERSONA_TOOL_SET=supervise (the manager narrows the list it gives), and the built-ins
// roundBuiltinTools.
//
// Every assistant.send says which tool set it needs: supervise_round asks for the round's, and its absence (the owner's
// message, a supervision message once assistant.background_supervisor_keeps_context is on, a manager from before
// #7315) for every tool. A round's process that is idle when a message needing every tool comes is replaced by a plain
// --resume with every tool and no round state, so it is compacted when idle again. Replacing one still working (a turn,
// one Claude Code started, background tasks, a compaction, a message it queued: workingLocked) would lose that work,
// and writing the message into it would run it on the round's tools, so the node holds the message
// (assistantProc.held) outside Claude Code's queue, answering its send as delivered. Once the process is idle (and
// stayed so for assistantHeldSettle), the first held message replaces it with every tool and the others follow into
// the new process, in order (deliverHeld). Held messages are kept as Claude Code's own queue is: with the process, which
// a resident node keeps across its connections and does not restart under (AssistantsBusy); a process that exits
// meanwhile hands them to the next one. A round (supervise_round) is not held: it goes into the working process as
// before (superviseRound, roundWhyBusy), as does a recovery's retry of the round's turn.
//
// --resume-session-at is a print-mode flag Claude Code does not document. It is used only where it was measured: Claude
// Code on the CLI (not Codex, the Agent SDK runtime, another runtime or a guest), roundMinClaude or later, an idle
// process with no background tasks, and an anchor found on the transcript's latest chain (roundAnchor). Anything else
// sends as before and says why in the answer (context "kept"); a round whose process does not start is sent once more
// as before. A round's process has started when it prints its init (readAssistant): one that refuses the anchor prints
// only a result with no turn and exits 1.

// The answer's context: the round started from the first turn (fresh), or went in as before (kept), and why.
const (
	roundFresh          = "fresh"
	roundKept           = "kept"
	roundWhyBusy        = "busy"         // the process works or holds messages (workingLocked, hold): the round joins it
	roundWhyUnsupported = "unsupported"  // not Claude Code on the CLI, or one older than roundMinClaude
	roundWhyNoAnchor    = "no_anchor"    // no first turn on the latest chain (a compaction cut it off, or none ended)
	roundWhyStartFailed = "start_failed" // the round's process did not start: resumed as before
)

// roundMinClaude is the first Claude Code --resume-session-at was measured on (2.1.322); older ones keep the context.
var roundMinClaude = [3]int{2, 1, 322}

// roundBuiltinTools are a round's built-in tools: what supervision uses (reading evidence, gh in Bash, a skill), none of
// the others the assistant gets (assistantBuiltinTools), and ToolSearch, which --tools filters too: with nothing
// deferred Claude Code lists no ToolSearch (measured on 2.1.322, where leaving it out added a DeferredToolPlaceholder
// tool instead), and a Persona tool the manager defers can still be loaded (#5839).
const roundBuiltinTools = "Bash,Read,Write,Skill,ToolSearch"

// roundToolSet names the round's tool set to the manager (PERSONA_TOOL_SET, mcp.tools tool_set).
const roundToolSet = "supervise"

// spawnRound is how a process starts for a round: resumed at anchor, with tool set toolSet. The zero value is an
// ordinary start, with every tool: what any message that is no round starting from the first turn needs.
type spawnRound struct{ anchor, toolSet string }

// roundResult is what became of a round asked for by one assistant.send, for its answer.
type roundResult struct {
	mu      sync.Mutex
	context string // roundFresh or roundKept
	why     string
	bytes   int64 // the transcript's size when the round was decided (the manager replaces an outgrown one)
	decided bool  // false while nothing was decided (the send did not run)
}

type roundKey struct{}

// withSuperviseRound marks ctx as an assistant.send of a round, with what becomes of it.
func withSuperviseRound(ctx context.Context) (context.Context, *roundResult) {
	r := &roundResult{}
	return context.WithValue(ctx, roundKey{}, r), r
}

// superviseRoundFrom is the round ctx's send was asked for, nil for any other send.
func superviseRoundFrom(ctx context.Context) *roundResult {
	r, _ := ctx.Value(roundKey{}).(*roundResult)
	return r
}

func (r *roundResult) set(context, why string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.context, r.why, r.decided = context, why, true
	r.mu.Unlock()
}

func (r *roundResult) sized(n int64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.bytes = n
	r.mu.Unlock()
}

// answer puts the round's outcome on the send's answer: context, why when it was kept, the transcript's size. Nothing
// when no round was asked for or the send did not run (the same once_id asked again).
func (r *roundResult) answer(out map[string]any) map[string]any {
	if r == nil {
		return out
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.decided {
		return out
	}
	out["context"] = r.context
	if r.why != "" {
		out["why"] = r.why
	}
	if r.bytes > 0 {
		out["transcript_bytes"] = r.bytes
	}
	return out
}

// superviseRound decides how the round r for conversation id goes in: from its first turn (a spawnRound with the anchor,
// after which the caller stops p, if any, and starts anew) or as before (the zero spawnRound, r says why). p is the
// conversation's process (nil for none), agent the launcher's kind, guest whether it is a guest conversation and
// execution what it runs on.
func (n *Node) superviseRound(ctx context.Context, r *roundResult, id string, p *assistantProc, agent string, guest bool, execution modelExecution) spawnRound {
	kept := func(why string) spawnRound {
		r.set(roundKept, why)
		n.logf("assistant %s: supervision round goes in as before (%s)", short(id), why)
		return spawnRound{}
	}
	if id == "" || agent != "claude" || guest || execution.Runtime != "" {
		return kept(roundWhyUnsupported)
	}
	version := ""
	if p != nil && p.alive() {
		p.mu.Lock()
		busy := p.workingLocked() || len(p.held) > 0 // messages it holds go in first (deliverHeld)
		other := p.agent != "claude" || p.runtime != ""
		version = p.claudeVersion
		p.mu.Unlock()
		if other {
			return kept(roundWhyUnsupported)
		}
		if busy {
			return kept(roundWhyBusy)
		}
	}
	path := n.transcriptPath(id)
	if path == "" {
		return kept(roundWhyNoAnchor)
	}
	tr, err := roundAnchor(path)
	if err != nil {
		n.logf("assistant %s: read its transcript for the round: %v", short(id), err)
		return kept(roundWhyNoAnchor)
	}
	r.sized(tr.size)
	if version = cmp.Or(version, tr.version); version == "" {
		version = n.claudeV.get(ctx)
	}
	if !versionAtLeast(version, roundMinClaude) {
		return kept(roundWhyUnsupported)
	}
	if tr.anchor == "" {
		return kept(roundWhyNoAnchor)
	}
	r.set(roundFresh, "")
	return spawnRound{anchor: tr.anchor, toolSet: roundToolSet}
}

// workingLocked: stopping the process now would lose something it runs or holds (p.mu held): a turn, a background
// task, a compaction (an idle one, or the user's, which sets no busy flag, assistant_compact.go), or a message Claude
// Code queued and has not started. A turn's result clears busy before the queued message's turn starts
// (assistant_queue.go), and a new process would never get that message; once Claude Code says it started, the turn is
// busy (commandLifecycle), as is a turn Claude Code starts on its own from its init (readAssistant). A round goes in as
// before then (superviseRound), and a message that needs every tool waits (hold).
func (p *assistantProc) workingLocked() bool {
	return p.busy || len(p.tasks) > 0 || p.compacting || p.manual || slices.ContainsFunc(p.written, func(w assistantInput) bool { return w.queued })
}

// heldSend is a message that needs every tool, held while a round's process works: what its send was asked, to send
// it again once the process is idle (deliverHeld).
type heldSend struct {
	ctx                 context.Context // its send's values, not its deadline
	cwd, launcher, text string
	images              []proto.Image
	mode, effort        string
	options             []modelExecution
}

// assistantHeldSettle is how long a round's process holding messages stays idle before they go in. Claude Code starts
// a turn of its own for a background task's notification right after a turn's result or its task list emptying
// (measured on 2.1.322: its init 7 ms after the result, 26 ms after the list emptied), and nothing says so before that
// init: replacing the process in between would cut that turn off.
var assistantHeldSettle = time.Second

// assistantHeldSendWait bounds the send of one held message (deliverHeld).
const assistantHeldSendWait = 2 * time.Minute

type heldKey struct{}

// heldDelivery says ctx's send delivers a held message (deliverHeld), which holds the conversation's typing lock.
func heldDelivery(ctx context.Context) bool {
	v, _ := ctx.Value(heldKey{}).(bool)
	return v
}

// errHeldAgain: the process a held message was being delivered to works again; the message is held once more.
var errHeldAgain = errors.New("the round's process works again: the message waits")

// hold keeps h until p is idle, behind the messages held before it (first: at their front, a delivery put back).
func (n *Node) hold(p *assistantProc, h heldSend, first bool) {
	h.ctx = context.WithoutCancel(h.ctx)
	p.mu.Lock()
	if first {
		p.held = append([]heldSend{h}, p.held...)
	} else {
		p.held = append(p.held, h)
	}
	one := len(p.held) == 1
	p.mu.Unlock()
	if one && !first {
		n.logf("assistant %s: a supervision round's process is still working: the message waits until it is idle, then goes in with every tool", short(p.id))
	}
	n.deliverHeldWhenIdle(p) // idle already, the messages before it still on their way
}

// deliverHeldWhenIdle starts delivering p's held messages (deliverHeld) when it has some, none is on its way, and p is
// idle or has exited.
func (n *Node) deliverHeldWhenIdle(p *assistantProc) {
	p.mu.Lock()
	start := len(p.held) > 0 && !p.delivering && (!p.alive() || !p.workingLocked())
	if start {
		p.delivering = true
	}
	p.mu.Unlock()
	if start {
		go n.deliverHeld(p)
	}
}

// deliverHeld sends p's held messages in order once p has stayed idle for assistantHeldSettle, or exited: the first
// replaces p with a plain --resume with every tool (assistantSend), the others go into that process. A turn that started
// meanwhile keeps them held until it ends. Under the conversation's typing lock, so a message sent meanwhile goes in
// after them. A Node whose connection ended leaves them to the one that takes p over (resumeAssistants).
func (n *Node) deliverHeld(p *assistantProc) {
	select {
	case <-time.After(assistantHeldSettle):
	case <-p.exited:
	}
	n = p.ownerOr(n)
	n.mu.Lock()
	closed := n.recoveryClosed
	n.mu.Unlock()
	if !closed {
		defer n.typing.lock(p.id)()
	}
	for {
		p.mu.Lock()
		if closed || len(p.held) == 0 || p.alive() && p.workingLocked() {
			p.delivering = false
			p.mu.Unlock()
			return
		}
		h := p.held[0]
		p.held = p.held[1:]
		p.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.WithValue(h.ctx, heldKey{}, true), assistantHeldSendWait)
		_, err := n.assistantSend(ctx, p.id, h.cwd, h.launcher, h.text, h.images, h.mode, h.effort, h.options...)
		cancel()
		if errors.Is(err, errHeldAgain) {
			p.mu.Lock()
			p.delivering = false
			p.mu.Unlock()
			n.deliverHeldWhenIdle(p) // its work may have ended meanwhile
			return
		}
		if err != nil {
			n.logf("assistant %s: a message held for a supervision round's process did not go in: %v", short(p.id), err)
		}
	}
}

// roundTranscript is what roundAnchor read in a transcript: the anchor ("" for none), the Claude Code version that wrote
// its last line, and its size.
type roundTranscript struct {
	anchor, version string
	size            int64
}

// roundLine is what roundAnchor reads of a line.
type roundLine struct {
	Type      string  `json:"type"`
	UUID      string  `json:"uuid"`
	Parent    *string `json:"parentUuid"`
	Sidechain bool    `json:"isSidechain"`
	Meta      bool    `json:"isMeta"`
	Summary   bool    `json:"isCompactSummary"`
	Version   string  `json:"version"`
	Message   *struct {
		StopReason string          `json:"stop_reason"`
		Content    json.RawMessage `json:"content"`
	} `json:"message"`
}

// prompt says the line is a message someone wrote to the conversation: a user line that is no tool result, meta line
// or compaction summary.
func (l roundLine) prompt() bool {
	if l.Type != "user" || l.Sidechain || l.Meta || l.Summary || l.Message == nil {
		return false
	}
	c := bytes.TrimSpace(l.Message.Content)
	if len(c) > 0 && c[0] == '"' {
		return true
	}
	var blocks []struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(c, &blocks) != nil {
		return false
	}
	for _, b := range blocks {
		if b.Type == "tool_result" {
			return false
		}
	}
	return len(blocks) > 0
}

// roundAnchor finds where a round resumes in the transcript at path: the last assistant line that ended a turn
// (stop_reason end_turn) before the second message, so the round reads the start message and its answer, not the start
// message merged into its own prompt. It counts only when it lies on the chain Claude Code resumes, walked back from
// the last user or assistant line: a compaction starts a new chain (its first turn is no longer an ancestor, and
// Claude Code refuses the anchor), and so does a file whose chain is broken. Sidechain lines (subagents) are not the
// conversation's.
func roundAnchor(path string) (roundTranscript, error) {
	f, err := os.Open(path)
	if err != nil {
		return roundTranscript{}, err
	}
	defer f.Close()
	var out roundTranscript
	if st, err := f.Stat(); err == nil {
		out.size = st.Size()
	}
	up := map[string]string{} // line → its parent ("" for a chain's start)
	messages := 0
	candidate, leaf := "", ""
	r := bufio.NewReaderSize(f, 64<<10)
	for {
		line, err := r.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var l roundLine
			if json.Unmarshal(line, &l) == nil {
				if l.Version != "" {
					out.version = l.Version
				}
				if l.UUID != "" && !l.Sidechain {
					if _, seen := up[l.UUID]; !seen {
						parent := ""
						if l.Parent != nil {
							parent = *l.Parent
						}
						up[l.UUID] = parent
					}
					if l.prompt() {
						messages++
					}
					if l.Type == "user" || l.Type == "assistant" {
						leaf = l.UUID
					}
					if messages == 1 && l.Type == "assistant" && l.Message != nil && l.Message.StopReason == "end_turn" {
						candidate = l.UUID
					}
				}
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return out, err
		}
	}
	if candidate == "" || leaf == "" {
		return out, nil
	}
	seen := map[string]bool{}
	for u := leaf; u != "" && !seen[u]; u = up[u] {
		if u == candidate {
			out.anchor = candidate
			break
		}
		seen[u] = true
	}
	return out, nil
}
