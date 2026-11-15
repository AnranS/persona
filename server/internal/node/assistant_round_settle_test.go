package node

import (
	"log"
	"strings"
	"testing"
	"time"
)

// What the node does with a round's process (#7315, assistant_round.go) when it did not settle in time.

// statusLine is a line Claude Code prints that is no work (a status update): it keeps a process from settling and makes
// it no busier.
const statusLine = `{"type":"system","subtype":"status","status":null}`

// keepPrinting has p print statusLine now and every 50 ms after, as a process that writes without working, until
// done() holds or stop closes. The channel it returns closes once p printed its last line.
func keepPrinting(n *Node, p *assistantProc, done func() bool, stop <-chan struct{}) <-chan struct{} {
	feedAssistant(n, p, statusLine) // before anything looks at p
	quiet := make(chan struct{})
	go func() {
		defer close(quiet)
		tick := time.NewTicker(50 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
			}
			if done() {
				return
			}
			feedAssistant(n, p, statusLine)
		}
	}()
	return quiet
}

// A fresh round that finds the round's process printing all the while the node waits for it to settle (stopSettled)
// goes in as before, kept as busy, and joins that process as its answer says: the process is not replaced for the
// message's tools once it went quiet, which would run the round in a new plain process instead. A round kept for
// another reason (no anchor on the latest chain) needs every tool like any message, and replaces the settled process.
func TestARoundKeptAsBusyJoinsTheRoundProcess(t *testing.T) {
	t.Run("printing while it settles", func(t *testing.T) {
		n, argv, input := roundTestNode(t, roundStarted("2.1.322", 1), false)
		var logs syncBuffer
		n.logger = log.New(&logs, "", 0)
		round := roundThenIdle(t, n)
		kept := func() bool {
			return strings.Contains(logs.String(), "supervision round goes in as before ("+roundWhyBusy+")")
		}
		stop := make(chan struct{})
		quiet := keepPrinting(n, round, kept, stop) // until the node gave up waiting: quiet from then on
		out := sendRound(t, n, "once-printing")
		close(stop)
		<-quiet
		if out["context"] != roundKept || out["why"] != roundWhyBusy {
			t.Fatalf("the round: %v", out)
		}
		waitFor(t, "the round in a process", func() bool { return len(roundInputs(t, input)) == 2 })
		stillRound(t, n, round, argv, 1)
		round.mu.Lock()
		retire := round.retire
		round.mu.Unlock()
		if retire {
			t.Fatal("the round's own message marked its process to retire")
		}
	})
	t.Run("no anchor", func(t *testing.T) {
		n, argv, input := roundTestNode(t, roundStarted("2.1.322", 1), false)
		round := roundThenIdle(t, n)
		compacted := roundStarted("2.1.322", 1)
		compacted.add(map[string]any{"type": "system", "subtype": "compact_boundary", "uuid": "cb", "parentUuid": nil}).
			user("r2", "cb", "📋 round 2").reply("r2a", "r2", "end_turn")
		compacted.write(t, n.transcriptPath(roundConversation))
		if out := sendRound(t, n, "once-unanchored"); out["context"] != roundKept || out["why"] != roundWhyNoAnchor {
			t.Fatalf("the round: %v", out)
		}
		all := roundStarts(t, argv)
		if len(all) != 2 {
			t.Fatalf("%d starts: %q", len(all), all)
		}
		everyToolStart(t, all[1])
		waitFor(t, "the round's process to end", func() bool { return !round.alive() })
		waitFor(t, "the round in the new process", func() bool { return len(roundInputs(t, input)) == 2 })
		if p := roundProc(n); p == round || p.round {
			t.Fatal("the round went to a round's process")
		}
	})
}
