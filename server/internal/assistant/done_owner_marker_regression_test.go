package assistant

import (
	"context"
	"testing"

	"github.com/nwverse/persona/server/internal/prompts"
)

// Conditional guidance and quoted history are not new owner demands; independent direct demands still get through.
func TestOwnerMarkerConditionalAndQuotedOccurrences(t *testing.T) {
	for _, tc := range []struct {
		name, reply, verdict string
	}{
		{"only if is conditional", "The work is complete. Only if the release fails, you need to roll it back.", verdictDone},
		{"trailing if is conditional", "The work is complete. You need to roll it back if the release fails.", verdictDone},
		{"trailing only if is conditional", "The work is complete. You need to roll it back only if the release fails.", verdictDone},
		{"trailing even if is still direct", "The work is complete. You need to notify the owner even if the release fails.", verdictNeedsUser},
		{"trailing if does not hide an independent requirement", "You need to roll it back if the release fails. Otherwise, you need to approve the release.", verdictNeedsUser},
		{"trailing if-question is a direct requirement", "The work is complete. You need to check if the release failed.", verdictNeedsUser},
		{"trailing conditional notification is not immediate", "The work is complete. You need to let me know if the release fails.", verdictDone},
		{"otherwise after only if is direct", "Only if the release fails, you need to roll it back. Otherwise, you need to approve the release.", verdictNeedsUser},
		{"nested only-if question does not hide a direct requirement", "The report checks only if the deploy succeeded: you need to approve the release.", verdictNeedsUser},
		{"quoted direct requirement is historical", `The old runbook said, "Stop. You need to approve the release." That approval is already done.`, verdictDone},
		{"quoted direct requirement does not hide a later one", `The old runbook said, "Stop. You need to roll back." You need to approve the release.`, verdictNeedsUser},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &cannedJSON{answer: `{"verdict":"done","reason":"finished"}`}
			c, err := judgeTaskDoneWith(context.Background(), m, taskDoneInput{Title: "Write the spec", Replies: []prompts.TaskDoneReply{doneReply(tc.reply)}})
			if err != nil {
				t.Fatal(err)
			}
			if c.Verdict != tc.verdict || c.WouldClose {
				t.Fatalf("reply %q: verdict %q close %v, want %q false", tc.reply, c.Verdict, c.WouldClose, tc.verdict)
			}
		})
	}
}
